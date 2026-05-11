package library

import (
	"bufio"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SymbolPoint is one (x, y) coordinate in canvas-local space.
type SymbolPoint struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
}

// SymbolRect is a width/height pair in canvas-local space. Symbols are drawn
// centred at (0, 0); the rect is the visible halo's outer bbox.
type SymbolRect struct {
	W float64 `yaml:"w" json:"w"`
	H float64 `yaml:"h" json:"h"`
}

// SymbolPin is one connection point on a symbol, in canvas-local coordinates.
// LabelSide is one of "none|top|bottom|left|right" — the wiring layer uses it
// to position the pin name label when zoomed in (deferred to a later phase).
type SymbolPin struct {
	Name      string  `yaml:"name"                 json:"name"`
	X         float64 `yaml:"x"                    json:"x"`
	Y         float64 `yaml:"y"                    json:"y"`
	LabelSide string  `yaml:"label_side,omitempty" json:"label_side,omitempty"`
}

// SymbolDef is the structured rendering of an LTspice .asy after the
// coordinate transform in §5 of symbol_enhancement.md. Body is an opaque SVG
// fragment that draws around (0, 0) — the schematic canvas mounts it inside a
// <g transform="translate rotate"> wrapper, mirroring the JSX SYMBOLS contract.
//
// Description / ModelFile / SpiceModel are passed through from the .asy's
// SYMATTR block so the importer can match a symbol against its companion .lib
// (.asy → .subckt) without re-reading the file.
type SymbolDef struct {
	BBox        SymbolRect  `yaml:"bbox"                  json:"bbox"`
	Origin      SymbolPoint `yaml:"origin"                json:"origin"`
	Pins        []SymbolPin `yaml:"pins"                  json:"pins"`
	Body        string      `yaml:"body"                  json:"body"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	ModelFile   string      `yaml:"model_file,omitempty"  json:"model_file,omitempty"`
	SpiceModel  string      `yaml:"spice_model,omitempty" json:"spice_model,omitempty"`
	Source      string      `yaml:"source,omitempty"      json:"source,omitempty"`
}

// asyParser collects shapes / pins / attrs while scanning the file. Public API
// is ParseAsy — the parser type itself is unexported.
type asyParser struct {
	version    int
	symbolType string
	geom       []asyShape
	pins       []asyPin
	attrs      map[string]string
	pendingPin *asyPin
}

type asyShape struct {
	kind           string  // "line", "rect", "circle", "arc"
	style          string  // Normal | Dotted | Dashed | Dashed_Dotted
	x1, y1, x2, y2 float64 // bbox corners (LINE/RECT/CIRCLE) or arc bbox
	sx, sy, ex, ey float64 // arc-only ray endpoints
}

type asyPin struct {
	x, y       float64
	orient     string
	name       string
	spiceOrder int
}

// ParseAsy converts an LTspice symbol file into a SymbolDef. The conversion is
// pure — no I/O, no Loader coupling — so callers (Loader.Import, tests) can
// invoke it directly with an in-memory body.
//
// Returns a structured error on:
//   - missing or non-4 Version line
//   - PIN without SpiceOrder
//   - empty geometry (no shapes and no pins)
//   - pins that snap to the same canvas coordinate (would be unwireable)
func ParseAsy(body string) (*SymbolDef, error) {
	p := &asyParser{attrs: map[string]string{}}
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if err := p.parseLine(trimmed); err != nil {
			return nil, fmt.Errorf("library: .asy line %d: %w", lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("library: read .asy: %w", err)
	}
	p.flushPendingPin()
	if p.version == 0 {
		return nil, fmt.Errorf("library: .asy: missing Version line")
	}
	if p.version != 4 {
		return nil, fmt.Errorf("library: .asy: unsupported Version %d (only 4)", p.version)
	}
	if len(p.pins) == 0 {
		return nil, fmt.Errorf("library: .asy: no PIN definitions")
	}
	for _, pin := range p.pins {
		if pin.spiceOrder == 0 {
			return nil, fmt.Errorf("library: .asy: pin %q missing SpiceOrder", pin.name)
		}
	}
	return p.toSymbol()
}

func (p *asyParser) parseLine(line string) error {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return nil
	}
	head := strings.ToUpper(tokens[0])
	switch head {
	case "VERSION":
		if len(tokens) < 2 {
			return fmt.Errorf("VERSION expects a number")
		}
		v, err := strconv.Atoi(tokens[1])
		if err != nil {
			return fmt.Errorf("VERSION %q: %w", tokens[1], err)
		}
		p.version = v
	case "SYMBOLTYPE":
		if len(tokens) >= 2 {
			p.symbolType = strings.ToUpper(tokens[1])
		}
	case "LINE":
		shape, err := parseShapeFour("line", tokens[1:])
		if err != nil {
			return err
		}
		p.geom = append(p.geom, shape)
	case "RECTANGLE":
		shape, err := parseShapeFour("rect", tokens[1:])
		if err != nil {
			return err
		}
		p.geom = append(p.geom, shape)
	case "CIRCLE":
		shape, err := parseShapeFour("circle", tokens[1:])
		if err != nil {
			return err
		}
		p.geom = append(p.geom, shape)
	case "ARC":
		shape, err := parseArc(tokens[1:])
		if err != nil {
			return err
		}
		p.geom = append(p.geom, shape)
	case "WINDOW":
		// label placement directives — owned by the frontend label pass.
	case "TEXT":
		// embedded literal text (rare). Phase 2 follow-up; safe to skip.
	case "SYMATTR":
		if len(tokens) < 2 {
			return nil
		}
		key := tokens[1]
		val := ""
		if len(tokens) >= 3 {
			// Re-read the original line so values with internal spaces survive
			// (Description routinely contains URLs and \n escapes).
			rest := line[len(tokens[0]):]
			rest = strings.TrimLeft(rest, " \t")
			rest = rest[len(tokens[1]):]
			val = strings.TrimSpace(rest)
		}
		p.attrs[strings.ToUpper(key)] = val
	case "PIN":
		if len(tokens) < 4 {
			return fmt.Errorf("PIN expects x y orient size")
		}
		x, err := strconv.ParseFloat(tokens[1], 64)
		if err != nil {
			return fmt.Errorf("PIN x: %w", err)
		}
		y, err := strconv.ParseFloat(tokens[2], 64)
		if err != nil {
			return fmt.Errorf("PIN y: %w", err)
		}
		p.flushPendingPin()
		p.pendingPin = &asyPin{x: x, y: y, orient: tokens[3]}
	case "PINATTR":
		if p.pendingPin == nil {
			return fmt.Errorf("PINATTR without preceding PIN")
		}
		if len(tokens) < 3 {
			return fmt.Errorf("PINATTR expects key value")
		}
		key := strings.ToUpper(tokens[1])
		val := strings.Join(tokens[2:], " ")
		switch key {
		case "PINNAME":
			p.pendingPin.name = val
		case "SPICEORDER":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("SpiceOrder %q: %w", val, err)
			}
			if n == 0 {
				return fmt.Errorf("SpiceOrder must be >= 1, got %d", n)
			}
			p.pendingPin.spiceOrder = n
		}
	default:
		// Unknown directive — skip silently. The .asy format is permissive and
		// we'd rather pass through forward-compat fields than error.
	}
	return nil
}

func (p *asyParser) flushPendingPin() {
	if p.pendingPin != nil {
		p.pins = append(p.pins, *p.pendingPin)
		p.pendingPin = nil
	}
}

func parseShapeFour(kind string, tokens []string) (asyShape, error) {
	if len(tokens) < 5 {
		return asyShape{}, fmt.Errorf("%s expects style x1 y1 x2 y2", kind)
	}
	nums, err := parseFloats(tokens[1:5])
	if err != nil {
		return asyShape{}, fmt.Errorf("%s coords: %w", kind, err)
	}
	return asyShape{kind: kind, style: tokens[0], x1: nums[0], y1: nums[1], x2: nums[2], y2: nums[3]}, nil
}

func parseArc(tokens []string) (asyShape, error) {
	if len(tokens) < 9 {
		return asyShape{}, fmt.Errorf("ARC expects style bx1 by1 bx2 by2 sx sy ex ey")
	}
	nums, err := parseFloats(tokens[1:9])
	if err != nil {
		return asyShape{}, fmt.Errorf("ARC coords: %w", err)
	}
	return asyShape{
		kind: "arc", style: tokens[0],
		x1: nums[0], y1: nums[1], x2: nums[2], y2: nums[3],
		sx: nums[4], sy: nums[5], ex: nums[6], ey: nums[7],
	}, nil
}

func parseFloats(tokens []string) ([]float64, error) {
	out := make([]float64, len(tokens))
	for i, t := range tokens {
		v, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// toSymbol applies the §5 transform pipeline: source-bbox → uniform scale →
// translate to centre → grid-snap pins → emit SVG body. Returns a fully-formed
// SymbolDef ready to attach to a manifest.
func (p *asyParser) toSymbol() (*SymbolDef, error) {
	first := true
	var minX, minY, maxX, maxY float64
	consider := func(x, y float64) {
		if first {
			minX, minY, maxX, maxY = x, y, x, y
			first = false
			return
		}
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	for _, s := range p.geom {
		consider(s.x1, s.y1)
		consider(s.x2, s.y2)
		if s.kind == "arc" {
			consider(s.sx, s.sy)
			consider(s.ex, s.ey)
		}
	}
	for _, pin := range p.pins {
		consider(pin.x, pin.y)
	}
	if first {
		return nil, fmt.Errorf("library: .asy: empty geometry")
	}
	srcW := maxX - minX
	srcH := maxY - minY
	if srcW <= 0 {
		srcW = 1
	}
	if srcH <= 0 {
		srcH = 1
	}

	tw, th := pickTargetSize(p.symbolType, len(p.pins), srcW, srcH)
	scale := math.Min(tw/srcW, th/srcH)
	postW := srcW * scale
	postH := srcH * scale
	tx := -minX*scale - postW/2
	ty := -minY*scale - postH/2
	tr := func(x, y float64) (float64, float64) {
		return x*scale + tx, y*scale + ty
	}

	// Pins, sorted by SpiceOrder (the .subckt header pin order). Snap to the
	// schematic grid so wire endpoints land on grid intersections.
	const pinSnap = 3.5
	sorted := make([]asyPin, len(p.pins))
	copy(sorted, p.pins)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].spiceOrder < sorted[j].spiceOrder
	})

	// Authoring sanity: two pins at identical source coordinates are an error
	// the .asy file itself never expresses correctly — every importer should
	// flag it, since the routing layer would have no way to distinguish them.
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].x == sorted[j].x && sorted[i].y == sorted[j].y {
				return nil, fmt.Errorf("library: .asy: pins %q and %q share source coordinates", sorted[i].name, sorted[j].name)
			}
		}
	}

	pins := make([]SymbolPin, len(sorted))
	taken := map[[2]float64]int{}
	for i, src := range sorted {
		x, y := tr(src.x, src.y)
		x = snapToStep(x, pinSnap)
		y = snapToStep(y, pinSnap)
		key := [2]float64{x, y}
		if other, ok := taken[key]; ok {
			x2, y2 := tr(src.x, src.y)
			if math.Abs(x2-x) > math.Abs(y2-y) {
				x = snapToStep(x2, pinSnap/2)
			} else {
				y = snapToStep(y2, pinSnap/2)
			}
			key = [2]float64{x, y}
			if _, ok := taken[key]; ok {
				return nil, fmt.Errorf("library: .asy: pins %q and %q snap to the same coordinate", sorted[other].name, src.name)
			}
		}
		taken[key] = i
		pins[i] = SymbolPin{
			Name:      src.name,
			X:         round1(x),
			Y:         round1(y),
			LabelSide: orientToSide(src.orient),
		}
	}

	var b strings.Builder
	b.WriteString(`<g fill="none" stroke="currentColor" stroke-width="0.9">`)
	for _, s := range p.geom {
		emitShape(&b, s, tr)
	}
	b.WriteString(`</g>`)

	body, err := sanitiseSymbolSVG(b.String())
	if err != nil {
		return nil, err
	}

	return &SymbolDef{
		BBox:        SymbolRect{W: round1(postW), H: round1(postH)},
		Origin:      SymbolPoint{X: 0, Y: 0},
		Pins:        pins,
		Body:        body,
		Description: p.attrs["DESCRIPTION"],
		ModelFile:   p.attrs["MODELFILE"],
		SpiceModel:  p.attrs["SPICEMODEL"],
	}, nil
}

// pickTargetSize chooses canvas-space dimensions for the post-transform symbol.
// Defaults follow the existing R/L/C/triode conventions; aspect is swapped to
// match the source so a vertically-drawn .asy stays vertical.
func pickTargetSize(symType string, pinCount int, srcW, srcH float64) (float64, float64) {
	cell := strings.EqualFold(symType, "CELL")
	var w, h float64
	switch {
	case !cell && pinCount == 2:
		w, h = 22, 10
	case !cell && pinCount == 3:
		w, h = 30, 22
	case cell && pinCount == 2:
		w, h = 28, 18
	case cell && pinCount <= 4:
		w, h = 44, 28
	default:
		w, h = 48, 36
	}
	if (srcH > srcW) != (h > w) {
		w, h = h, w
	}
	return w, h
}

func snapToStep(v, step float64) float64 {
	return math.Round(v/step) * step
}

func round1(v float64) float64 {
	r := math.Round(v*10) / 10
	if r == 0 {
		return 0 // collapse -0 to 0 for stable output
	}
	return r
}

func orientToSide(s string) string {
	switch strings.ToUpper(s) {
	case "TOP":
		return "top"
	case "BOTTOM":
		return "bottom"
	case "LEFT":
		return "left"
	case "RIGHT":
		return "right"
	}
	return "none"
}

func emitShape(b *strings.Builder, s asyShape, tr func(float64, float64) (float64, float64)) {
	switch s.kind {
	case "line":
		x1, y1 := tr(s.x1, s.y1)
		x2, y2 := tr(s.x2, s.y2)
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s"%s/>`,
			fmtCoord(x1), fmtCoord(y1), fmtCoord(x2), fmtCoord(y2), styleAttr(s.style))
	case "rect":
		x1, y1 := tr(s.x1, s.y1)
		x2, y2 := tr(s.x2, s.y2)
		x := math.Min(x1, x2)
		y := math.Min(y1, y2)
		w := math.Abs(x2 - x1)
		h := math.Abs(y2 - y1)
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s"%s/>`,
			fmtCoord(x), fmtCoord(y), fmtCoord(w), fmtCoord(h), styleAttr(s.style))
	case "circle":
		x1, y1 := tr(s.x1, s.y1)
		x2, y2 := tr(s.x2, s.y2)
		cx := (x1 + x2) / 2
		cy := (y1 + y2) / 2
		rx := math.Abs(x2-x1) / 2
		ry := math.Abs(y2-y1) / 2
		if math.Abs(rx-ry) < 0.5 {
			r := (rx + ry) / 2
			fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s"%s/>`,
				fmtCoord(cx), fmtCoord(cy), fmtCoord(r), styleAttr(s.style))
		} else {
			fmt.Fprintf(b, `<ellipse cx="%s" cy="%s" rx="%s" ry="%s"%s/>`,
				fmtCoord(cx), fmtCoord(cy), fmtCoord(rx), fmtCoord(ry), styleAttr(s.style))
		}
	case "arc":
		emitArc(b, s, tr)
	}
}

func emitArc(b *strings.Builder, s asyShape, tr func(float64, float64) (float64, float64)) {
	bx1, by1 := tr(s.x1, s.y1)
	bx2, by2 := tr(s.x2, s.y2)
	cx := (bx1 + bx2) / 2
	cy := (by1 + by2) / 2
	rx := math.Abs(bx2-bx1) / 2
	ry := math.Abs(by2-by1) / 2
	if rx <= 0 || ry <= 0 {
		return
	}
	sxT, syT := tr(s.sx, s.sy)
	exT, eyT := tr(s.ex, s.ey)
	thS := math.Atan2((syT-cy)/ry, (sxT-cx)/rx)
	thE := math.Atan2((eyT-cy)/ry, (exT-cx)/rx)
	swept := math.Mod(thE-thS, 2*math.Pi)
	if swept < 0 {
		swept += 2 * math.Pi
	}
	// Degenerate full-circle inputs collapse to a no-op rather than emitting an
	// invalid `M = M` arc; LTspice itself wouldn't accept that input either.
	if swept == 0 {
		return
	}
	largeArc := 0
	if swept > math.Pi {
		largeArc = 1
	}
	startX := cx + rx*math.Cos(thS)
	startY := cy + ry*math.Sin(thS)
	endX := cx + rx*math.Cos(thE)
	endY := cy + ry*math.Sin(thE)
	fmt.Fprintf(b, `<path d="M %s %s A %s %s 0 %d 0 %s %s"%s/>`,
		fmtCoord(startX), fmtCoord(startY),
		fmtCoord(rx), fmtCoord(ry),
		largeArc,
		fmtCoord(endX), fmtCoord(endY),
		styleAttr(s.style))
}

func fmtCoord(v float64) string {
	v = round1(v)
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if strings.HasSuffix(s, ".0") {
		s = s[:len(s)-2]
	}
	return s
}

func styleAttr(style string) string {
	switch strings.ToLower(style) {
	case "dotted":
		return ` stroke-dasharray="0.5 1.5"`
	case "dashed":
		return ` stroke-dasharray="2 1.5"`
	case "dashed_dotted":
		return ` stroke-dasharray="2 1 0.5 1"`
	}
	return ""
}

// sanitiseSymbolSVG enforces the conservative allow-list from spec §10.7. The
// converter never produces unsafe markup, but any future code path that leaks
// user-supplied content into Body must still survive this pass — defence in
// depth ahead of dangerouslySetInnerHTML on the frontend.
var (
	asySVGAllowedElements = map[string]bool{
		"g": true, "line": true, "rect": true, "circle": true,
		"ellipse": true, "path": true,
	}
	asySVGAllowedAttrs = map[string]bool{
		"x": true, "y": true,
		"x1": true, "y1": true, "x2": true, "y2": true,
		"cx": true, "cy": true, "r": true, "rx": true, "ry": true,
		"width": true, "height": true,
		"d":                true,
		"fill":             true,
		"stroke":           true,
		"stroke-width":     true,
		"stroke-dasharray": true,
		"stroke-linecap":   true,
		"stroke-linejoin":  true,
	}
)

func sanitiseSymbolSVG(s string) (string, error) {
	i := 0
	for i < len(s) {
		if s[i] != '<' {
			i++
			continue
		}
		j := i + 1
		closing := false
		if j < len(s) && s[j] == '/' {
			closing = true
			j++
		}
		nameStart := j
		for j < len(s) && (isAlpha(s[j]) || s[j] == '-') {
			j++
		}
		name := strings.ToLower(s[nameStart:j])
		if name == "" || !asySVGAllowedElements[name] {
			return "", fmt.Errorf("library: .asy svg: disallowed element <%s>", name)
		}
		end := strings.IndexByte(s[j:], '>')
		if end < 0 {
			return "", fmt.Errorf("library: .asy svg: unterminated tag <%s>", name)
		}
		attrsRaw := s[j : j+end]
		if !closing {
			if err := checkAttrs(name, attrsRaw); err != nil {
				return "", err
			}
		}
		i = j + end + 1
	}
	return s, nil
}

func checkAttrs(name, raw string) error {
	r := strings.TrimSpace(raw)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSpace(r)
	for len(r) > 0 {
		eq := strings.IndexByte(r, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(r[:eq]))
		if !asySVGAllowedAttrs[key] {
			return fmt.Errorf("library: .asy svg: disallowed attribute %q on <%s>", key, name)
		}
		rest := strings.TrimSpace(r[eq+1:])
		if len(rest) == 0 || (rest[0] != '"' && rest[0] != '\'') {
			return fmt.Errorf("library: .asy svg: unquoted attribute on <%s>", name)
		}
		quote := rest[0]
		rest = rest[1:]
		end := strings.IndexByte(rest, quote)
		if end < 0 {
			return fmt.Errorf("library: .asy svg: unterminated attribute value on <%s>", name)
		}
		r = strings.TrimSpace(rest[end+1:])
	}
	return nil
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// looksLikeAsy is a cheap signature check Loader.Import uses to dispatch
// between .lib (subckt body) and .asy (symbol geometry) files. Returns true
// when the body's first non-blank, non-comment line begins with "Version".
func looksLikeAsy(body string) bool {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 4<<10), 8<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, ";") {
			continue
		}
		return strings.HasPrefix(strings.ToUpper(line), "VERSION ")
	}
	return false
}
