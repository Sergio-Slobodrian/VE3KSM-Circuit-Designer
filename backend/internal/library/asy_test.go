package library

import (
	"strings"
	"testing"
)

// fixtureWCAPAI3H is a 2-pin BLOCK symbol from the Würth WCAP-AI3H pack —
// vertically-oriented capacitor with the electrolytic mark and bottom arc.
const fixtureWCAPAI3H = `Version 4
SymbolType BLOCK
LINE Normal -32 27 -32 0
LINE Normal -32 37 -32 64
LINE Normal -16 27 -48 27
LINE Normal -36 16 -46 16
LINE Normal -41 22 -41 11
ARC Normal -58 37 -6 78 -19 41 -42 44
WINDOW 38 -17 52 Left 2
WINDOW 0 -17 1 Left 2
SYMATTR Description WCAP-AI3H Aluminum Electrolytic Capacitors
SYMATTR InstName C
SYMATTR SpiceModel 861140783006_2.2mF
SYMATTR Prefix x
SYMATTR ModelFile WCAP-AI3H.lib
PIN -32 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN -32 64 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`

// fixtureWECHSA is a 2-pin BLOCK symbol with four ARC primitives forming the
// inductor coils horizontally — exercises ARC-only geometry and short stub LINEs.
const fixtureWECHSA = `Version 4
SymbolType BLOCK
LINE Normal -32 0 -48 0
LINE Normal 48 0 32 0
ARC Normal -32 8 -16 -8 -16 0 -29 0
ARC Normal 0 8 16 -8 16 0 3 0
ARC Normal 16 8 32 -8 32 0 19 0
ARC Normal -16 8 0 -8 0 0 -13 0
SYMATTR Description WE-CHSA SMT High Current Inductor
SYMATTR SpiceModel 1011_7843330033_0.33u
SYMATTR ModelFile WE-CHSA.lib
PIN -48 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 48 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`

// fixtureWECNSA is a 4-pin CELL symbol — common-mode choke. Exercises
// out-of-order SpiceOrder, CIRCLE primitives, BOTTOM orient labels, and the
// CELL-vs-BLOCK target sizing branch.
const fixtureWECNSA = `Version 4
SymbolType CELL
LINE Normal -64 -48 -96 -48
LINE Normal -64 48 -96 48
LINE Normal 64 -48 96 -48
LINE Normal 64 48 96 48
LINE Normal 64 -64 -64 -64
LINE Normal 64 64 64 -64
LINE Normal -64 64 64 64
LINE Normal -64 -64 -64 64
LINE Normal 32 5 -32 5
LINE Normal 32 -5 -32 -5
CIRCLE Normal -37 -24 -43 -30
CIRCLE Normal -38 30 -44 24
ARC Normal -32 -32 -16 -16 -32 -24 -16 -24
ARC Normal 16 16 32 32 32 24 16 24
SYMATTR Description WE-CNSA SMD Common Mode Line Filter
SYMATTR SpiceModel 0805_784231061_67ohm
SYMATTR ModelFile WE-CNSA.lib
PIN -96 -48 BOTTOM 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN -96 48 BOTTOM 8
PINATTR PinName 3
PINATTR SpiceOrder 2
PIN 96 -48 BOTTOM 8
PINATTR PinName 2
PINATTR SpiceOrder 3
PIN 96 48 BOTTOM 8
PINATTR PinName 4
PINATTR SpiceOrder 4
`

func TestParseAsyVerticalCap(t *testing.T) {
	sym, err := ParseAsy(fixtureWCAPAI3H)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := len(sym.Pins), 2; got != want {
		t.Fatalf("pin count = %d, want %d", got, want)
	}
	// Source is taller than wide → target should be vertical (h > w).
	if sym.BBox.H <= sym.BBox.W {
		t.Errorf("expected vertical bbox (h > w), got %+v", sym.BBox)
	}
	if sym.Pins[0].Name != "1" || sym.Pins[1].Name != "2" {
		t.Errorf("pin names = %q,%q, want 1,2", sym.Pins[0].Name, sym.Pins[1].Name)
	}
	// Both pins on the same x (centred), opposite y (top + bottom).
	if sym.Pins[0].X != sym.Pins[1].X {
		t.Errorf("vertical layout: pins should share x; got %.2f vs %.2f", sym.Pins[0].X, sym.Pins[1].X)
	}
	if sym.Pins[0].Y >= sym.Pins[1].Y {
		t.Errorf("expected pin1.Y < pin2.Y for top-then-bottom order, got %.2f vs %.2f", sym.Pins[0].Y, sym.Pins[1].Y)
	}
	if sym.Description == "" {
		t.Error("expected Description from SYMATTR")
	}
	if sym.ModelFile != "WCAP-AI3H.lib" {
		t.Errorf("ModelFile = %q, want WCAP-AI3H.lib", sym.ModelFile)
	}
	if sym.SpiceModel != "861140783006_2.2mF" {
		t.Errorf("SpiceModel = %q, want 861140783006_2.2mF", sym.SpiceModel)
	}
	if !strings.Contains(sym.Body, "<g") || !strings.Contains(sym.Body, "</g>") {
		t.Errorf("body missing <g> wrapper: %q", sym.Body)
	}
	if !strings.Contains(sym.Body, "<path") {
		t.Errorf("body missing <path> for ARC: %q", sym.Body)
	}
}

func TestParseAsyHorizontalInductor(t *testing.T) {
	sym, err := ParseAsy(fixtureWECHSA)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(sym.Pins) != 2 {
		t.Fatalf("pin count = %d, want 2", len(sym.Pins))
	}
	// Source wider than tall → horizontal target (w > h).
	if sym.BBox.W <= sym.BBox.H {
		t.Errorf("expected horizontal bbox (w > h), got %+v", sym.BBox)
	}
	// Pins should be on opposite x sides.
	if sym.Pins[0].X >= sym.Pins[1].X {
		t.Errorf("expected pin1.X < pin2.X; got %.2f >= %.2f", sym.Pins[0].X, sym.Pins[1].X)
	}
	// Both pins on y=0 axis (within snap tolerance).
	if sym.Pins[0].Y != 0 || sym.Pins[1].Y != 0 {
		t.Errorf("expected horizontal pins on y=0; got %.2f, %.2f", sym.Pins[0].Y, sym.Pins[1].Y)
	}
	// Should have multiple <path> elements (the four ARCs).
	if c := strings.Count(sym.Body, "<path"); c < 4 {
		t.Errorf("expected ≥4 <path> elements (one per ARC); got %d", c)
	}
}

func TestParseAsyFourPinChokeSpiceOrder(t *testing.T) {
	sym, err := ParseAsy(fixtureWECNSA)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(sym.Pins) != 4 {
		t.Fatalf("pin count = %d, want 4", len(sym.Pins))
	}
	wantNames := []string{"1", "3", "2", "4"} // sorted by SpiceOrder 1..4
	for i, p := range sym.Pins {
		if p.Name != wantNames[i] {
			t.Errorf("pin[%d].Name = %q, want %q", i, p.Name, wantNames[i])
		}
		if p.LabelSide != "bottom" {
			t.Errorf("pin[%d].LabelSide = %q, want bottom", i, p.LabelSide)
		}
	}
	// Pins should land at the four corners of the bbox.
	uniqueX := map[float64]bool{}
	uniqueY := map[float64]bool{}
	for _, p := range sym.Pins {
		uniqueX[p.X] = true
		uniqueY[p.Y] = true
	}
	if len(uniqueX) != 2 || len(uniqueY) != 2 {
		t.Errorf("expected 4 corner pins on 2 distinct x and 2 distinct y, got x=%v y=%v", uniqueX, uniqueY)
	}
	if !strings.Contains(sym.Body, "<circle") {
		t.Errorf("expected CIRCLE primitives to render as <circle>; body=%q", sym.Body)
	}
}

func TestParseAsyMissingVersion(t *testing.T) {
	_, err := ParseAsy("SymbolType BLOCK\nLINE Normal 0 0 10 0\nPIN 0 0 NONE 8\nPINATTR PinName 1\nPINATTR SpiceOrder 1\n")
	if err == nil {
		t.Fatal("expected error for missing Version")
	}
	if !strings.Contains(err.Error(), "Version") {
		t.Errorf("error %q should mention Version", err)
	}
}

func TestParseAsyUnsupportedVersion(t *testing.T) {
	_, err := ParseAsy("Version 5\nSymbolType BLOCK\nPIN 0 0 NONE 8\nPINATTR PinName 1\nPINATTR SpiceOrder 1\n")
	if err == nil {
		t.Fatal("expected error for Version 5")
	}
	if !strings.Contains(err.Error(), "unsupported Version") {
		t.Errorf("error %q should mention unsupported Version", err)
	}
}

func TestParseAsyMissingSpiceOrder(t *testing.T) {
	body := `Version 4
SymbolType BLOCK
LINE Normal 0 0 22 0
PIN 0 0 NONE 8
PINATTR PinName 1
PIN 22 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 1
`
	_, err := ParseAsy(body)
	if err == nil {
		t.Fatal("expected error for missing SpiceOrder")
	}
	if !strings.Contains(err.Error(), "SpiceOrder") {
		t.Errorf("error %q should mention SpiceOrder", err)
	}
}

func TestParseAsyNoPins(t *testing.T) {
	_, err := ParseAsy("Version 4\nSymbolType BLOCK\nLINE Normal 0 0 22 0\n")
	if err == nil {
		t.Fatal("expected error for no PIN definitions")
	}
}

func TestParseAsyStyleMapping(t *testing.T) {
	body := `Version 4
SymbolType BLOCK
LINE Dotted 0 0 22 0
LINE Dashed 0 4 22 4
LINE Dashed_Dotted 0 8 22 8
PIN 0 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 22 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`
	sym, err := ParseAsy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(sym.Body, `stroke-dasharray="0.5 1.5"`) {
		t.Errorf("missing dotted dasharray: %q", sym.Body)
	}
	if !strings.Contains(sym.Body, `stroke-dasharray="2 1.5"`) {
		t.Errorf("missing dashed dasharray: %q", sym.Body)
	}
	if !strings.Contains(sym.Body, `stroke-dasharray="2 1 0.5 1"`) {
		t.Errorf("missing dashed-dotted dasharray: %q", sym.Body)
	}
}

func TestParseAsyArcEmitsSvgPath(t *testing.T) {
	body := `Version 4
SymbolType BLOCK
ARC Normal -10 -10 10 10 10 0 0 10
PIN -10 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 10 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`
	sym, err := ParseAsy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(sym.Body, `<path d="M `) || !strings.Contains(sym.Body, ` A `) {
		t.Errorf("ARC should emit a path with M and A commands: %q", sym.Body)
	}
}

func TestParseAsyOrientToLabelSide(t *testing.T) {
	body := `Version 4
SymbolType CELL
LINE Normal 0 0 40 0
PIN 0 0 TOP 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 40 0 RIGHT 8
PINATTR PinName 2
PINATTR SpiceOrder 2
PIN 0 20 LEFT 8
PINATTR PinName 3
PINATTR SpiceOrder 3
PIN 40 20 BOTTOM 8
PINATTR PinName 4
PINATTR SpiceOrder 4
`
	sym, err := ParseAsy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantSides := []string{"top", "right", "left", "bottom"}
	for i, p := range sym.Pins {
		if p.LabelSide != wantSides[i] {
			t.Errorf("pin[%d].LabelSide = %q, want %q", i, p.LabelSide, wantSides[i])
		}
	}
}

func TestParseAsyPinSnapCollision(t *testing.T) {
	// Two pins so close that they snap to the same coordinate even at half-step.
	body := `Version 4
SymbolType BLOCK
LINE Normal 0 0 1000 0
PIN 100 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 100 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`
	_, err := ParseAsy(body)
	if err == nil {
		t.Fatal("expected error for colliding pins")
	}
	if !strings.Contains(err.Error(), "snap") && !strings.Contains(err.Error(), "coordinate") {
		t.Errorf("error %q should describe the collision", err)
	}
}

func TestSanitiseSymbolSVGAccepts(t *testing.T) {
	good := `<g fill="none" stroke="currentColor" stroke-width="0.9"><line x1="0" y1="0" x2="10" y2="0"/><circle cx="5" cy="5" r="2"/><path d="M0 0 A 5 5 0 0 0 10 10"/></g>`
	out, err := sanitiseSymbolSVG(good)
	if err != nil {
		t.Fatalf("sanitise: %v", err)
	}
	if out != good {
		t.Errorf("sanitiser should pass valid SVG through unchanged, got %q", out)
	}
}

func TestSanitiseSymbolSVGRejectsScript(t *testing.T) {
	cases := []string{
		`<script>alert(1)</script>`,
		`<g onload="alert(1)"></g>`,
		`<g><foreignobject></foreignobject></g>`,
		`<a href="evil"></a>`,
		`<image href="x"/>`,
	}
	for _, c := range cases {
		if _, err := sanitiseSymbolSVG(c); err == nil {
			t.Errorf("sanitiser should reject %q", c)
		}
	}
}

func TestLooksLikeAsyDistinguishesLib(t *testing.T) {
	asy := "* leading comment\nVersion 4\nSymbolType BLOCK\n"
	lib := "* WCAP-AI3H .lib\n.subckt FOO 1 2\n.ends FOO\n"
	if !looksLikeAsy(asy) {
		t.Error("looksLikeAsy should return true for a Version-4 .asy header")
	}
	if looksLikeAsy(lib) {
		t.Error("looksLikeAsy should return false for a .lib starting with .subckt")
	}
}

func TestParseAsyDescriptionPreservesSpaces(t *testing.T) {
	body := `Version 4
SymbolType BLOCK
LINE Normal 0 0 22 0
SYMATTR Description Aluminum Electrolytic Capacitors
PIN 0 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 22 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`
	sym, err := ParseAsy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sym.Description != "Aluminum Electrolytic Capacitors" {
		t.Errorf("Description = %q, want full multi-word value", sym.Description)
	}
}

func TestParseAsyCoordinateTransformCenters(t *testing.T) {
	// Manufactured input with bbox (-50,-30)..(50,30) — centred-symmetric.
	// After transform the bbox should be centred at (0, 0).
	body := `Version 4
SymbolType BLOCK
LINE Normal -50 -30 50 -30
LINE Normal -50 30 50 30
PIN -50 0 NONE 8
PINATTR PinName 1
PINATTR SpiceOrder 1
PIN 50 0 NONE 8
PINATTR PinName 2
PINATTR SpiceOrder 2
`
	sym, err := ParseAsy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Pins should be symmetric around x=0.
	if sym.Pins[0].X != -sym.Pins[1].X {
		t.Errorf("pins not centred on x: %.2f vs %.2f", sym.Pins[0].X, sym.Pins[1].X)
	}
	// And both on y=0 (snap).
	if sym.Pins[0].Y != 0 || sym.Pins[1].Y != 0 {
		t.Errorf("pins not on y=0: %.2f, %.2f", sym.Pins[0].Y, sym.Pins[1].Y)
	}
}
