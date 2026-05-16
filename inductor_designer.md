# Inductor Designer — DTO and Core Schema

Cross-project specification for the **Inductor Designer** module shared (in
spirit, not in code) between Circuit Lab and Antenna Studio. Both apps host
the feature as a full top-level tab. The Go math kernel is built here first,
then copied to Antenna Studio. The frontends are parallel implementations
(JSX in Circuit Lab, TS in Antenna Studio) speaking the same wire format.

This document defines:
1. The four design modes the module supports.
2. The HTTP DTOs (request + response) for each mode.
3. The core schema — bundled presets and user-defined cores.
4. Validation rules and the error model.
5. The initial bundled core library.

The Go types and frontend stores are derived from these definitions. JSON
field names are the canonical wire format — do not rename without updating
both apps.

---

## 1. Modes

| Mode       | Use case                                                                                                | Core required?                   |
| ---------- | ------------------------------------------------------------------------------------------------------- | -------------------------------- |
| `solenoid` | Air-cored or slug-tuned single-layer coil. Wheeler/Nagaoka with permeability scaling for non-air cores. | Optional (null = air)            |
| `toroid`   | Ring-core inductor. AL-based with per-material lookup.                                                  | **Required**                     |
| `spiral`   | Planar PCB spiral. Modified-Wheeler / Mohan, four shapes.                                               | No (substrate properties only)   |
| `coupled`  | Two windings sharing a core (transformer) or air-coupled. Computes M, k, leakage.                       | Per-winding (toroid or solenoid) |

A single `POST /api/inductor/design` endpoint dispatches on the `mode` field
of the request body. This keeps the surface area small and lets a frontend
swap modes by mutating a single field.

---

## 2. Request envelope

```json
{
  "mode": "solenoid" | "toroid" | "spiral" | "coupled",
  "frequency_hz": 7100000.0,
  "params": { ... mode-specific ... }
}
```

| Field          | Type   | Rule                                              |
| -------------- | ------ | ------------------------------------------------- |
| `mode`         | string | One of the four mode names                        |
| `frequency_hz` | number | > 0; used for Q, SRF check, frequency-dependent μ |
| `params`       | object | Shape determined by `mode` (see §3)               |

All physical quantities are **SI base units** (metres, henries, hertz, ohms,
teslas) at the wire layer. Frontend display conversion (mm, µH, MHz, AWG)
happens client-side. This rule is non-negotiable — it prevents the unit-
confusion class of bug that historically plagues coil calculators.

---

## 3. Mode-specific `params`

### 3.1 Solenoid

```json
{
  "turns": 25,
  "diameter_m": 0.010,
  "length_m": 0.020,
  "wire": { ... see §4 ... },
  "winding": "close_wound" | "spaced" | "single_layer",
  "pitch_m": 0.0007,
  "core": null | CoreRef
}
```

| Field        | Rule                                                                                                                                            |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `turns`      | > 0, fractional turns allowed for the math but a warning is emitted below 1                                                                     |
| `diameter_m` | Coil **form** diameter (inner side of the wire). > 0.                                                                                           |
| `length_m`   | Axial length of the wound section. > 0.                                                                                                         |
| `wire`       | A `WireSpec` (§4). Required even for air cores — needed for Q + DCR.                                                                            |
| `winding`    | `close_wound` → pitch = wire diameter; `spaced` → use `pitch_m`; `single_layer` is informational.                                               |
| `pitch_m`    | Required when `winding = "spaced"`. ≥ wire diameter.                                                                                            |
| `core`       | `null` for air; otherwise a `CoreRef` (§5). For solenoids, the core scales the result by an effective μ (open-circuit permeability < bulk μ_r). |

### 3.2 Toroid

```json
{
  "turns": 20,
  "core": CoreRef,
  "wire": WireSpec,
  "fill_check": true
}
```

| Field        | Rule                                                                                                                     |
| ------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `turns`      | Integer > 0. Toroid turns are always integer (a turn is a pass through the hole).                                        |
| `core`       | **Required.** Must reference a toroidal-geometry core.                                                                   |
| `wire`       | Required.                                                                                                                |
| `fill_check` | When `true`, response includes a window-fill warning if turns × wire-circumference exceeds the inner-hole circumference. |

### 3.3 Spiral (PCB)

```json
{
  "shape": "square" | "circular" | "hexagonal" | "octagonal",
  "turns": 8.5,
  "outer_diameter_m": 0.005,
  "inner_diameter_m": 0.002,
  "trace_width_m": 0.0002,
  "trace_spacing_m": 0.00015,
  "substrate": {
    "thickness_m": 0.0016,
    "epsilon_r": 4.4,
    "tan_delta": 0.02,
    "copper_thickness_m": 0.000035
  }
}
```

| Field                          | Rule                                                                                                                                                                 |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `shape`                        | Determines which K1/K2 coefficients to use (Mohan et al. 1999)                                                                                                       |
| `turns`                        | > 0, half-turn increments are physical (spiral ends mid-arc)                                                                                                         |
| `outer_diameter_m`             | OD across the outermost trace centerline                                                                                                                             |
| `inner_diameter_m`             | ID across the innermost trace centerline; must satisfy `outer > inner + 2·turns·(trace_width + trace_spacing) − margin`                                              |
| `trace_width_m`                | > 0                                                                                                                                                                  |
| `trace_spacing_m`              | > 0                                                                                                                                                                  |
| `substrate`                    | Used for parasitic capacitance → SRF, dielectric loss → Q, and thermal-current estimate                                                                              |
| `substrate.epsilon_r`          | Substrate relative permittivity. FR-4 ≈ 4.4, RO4003C ≈ 3.55, RO4350B ≈ 3.66.                                                                                         |
| `substrate.tan_delta`          | Substrate loss tangent. Contributes to Q via `Q_dielectric = 1/(2π·f·R_eq·C_par·tan_delta)`. FR-4 ≈ 0.02, RO4003C ≈ 0.0027, RO4350B ≈ 0.0037. Required — no default. |
| `substrate.copper_thickness_m` | Standard 1oz ≈ 35µm                                                                                                                                                  |

The frontend supplies common-laminate presets (FR-4, RO4003C, RO4350B) that
fill `epsilon_r` and `tan_delta` together; the wire format carries the raw
numbers so the kernel has no laminate-name awareness.

The total spiral Q combines conductor and dielectric losses:
`1/Q_total = 1/Q_conductor + 1/Q_dielectric`. Both contributions appear in
`details.q_conductor` and `details.q_dielectric` (see §6.1).

### 3.4 Coupled

```json
{
  "primary": { "mode": "solenoid" | "toroid", "params": { ... } },
  "secondary": { "mode": "solenoid" | "toroid", "params": { ... } },
  "shared_core": true,
  "geometry": "coaxial" | "side_by_side" | "stacked",
  "separation_m": 0.005,
  "coupling_k_override": null
}
```

| Field                   | Rule                                                                                                                                                                                  |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `primary` / `secondary` | Each is a nested solenoid- or toroid-mode descriptor (same params as §3.1 / §3.2)                                                                                                     |
| `shared_core`           | When `true`, both windings share the **same** core instance. Both must be the same mode (both toroid or both solenoid-on-slug). Coupling k is then geometry-derived (≈ 1 for toroid). |
| `geometry`              | Layout used to estimate k for **non-shared-core** cases. Ignored when `shared_core = true`.                                                                                           |
| `separation_m`          | Centre-to-centre for `coaxial`/`side_by_side`; gap for `stacked`. Required when `shared_core = false`.                                                                                |
| `coupling_k_override`   | If set (0 < k ≤ 1), bypasses the geometric estimate and uses this value directly.                                                                                                     |

Validation **must** reject `shared_core = true` with mismatched primary/
secondary modes (`primary.mode != secondary.mode`).

---

## 4. WireSpec (shared)

```json
{
  "diameter_m": 0.0005,
  "awg": null,
  "material": "copper" | "silver" | "aluminum",
  "insulation_thickness_m": 0.00003,
  "temperature_c": 25.0
}
```

Either `diameter_m` **or** `awg` must be supplied. If both are present,
`diameter_m` wins and `awg` is treated as a display hint only. The server
returns the wire diameter it actually used in the response (`details.wire`)
so the frontend can echo it back.

`temperature_c` is used for resistivity correction (copper α ≈ 0.00393/°C).
Default 25°C if omitted by the frontend.

**AWG table:** the Brown & Sharpe AWG diameters are baked into the kernel
as a Go literal (gauges 0000–40). The geometric formula
`d_inches = 0.005 × 92^((36−n)/39)` is mathematically frozen and has not
moved since 1857; ASTM B258 revisions are editorial. The user-extension
path is `wire.diameter_m` directly — no table reload, no signal handling.

**v1 scope:** solid round magnet wire only. Litz, square, ribbon, and
stranded wire are deferred. The kernel rejects unknown `material` values
rather than silently substituting.

---

## 5. CoreRef and CoreSpec

A core is either a reference to a bundled preset, or an inline user-defined
specification. Both forms share the same logical contents — the preset form
is just a server-side lookup that yields the same fields.

### 5.1 Preset reference

```json
{
  "kind": "preset",
  "id": "T-37-2"
}
```

The server looks `id` up in the bundled catalog (§7). If not found, the
response is a 400 with `error_code = "core.not_found"`.

### 5.2 User-defined inline core

```json
{
  "kind": "user",
  "geometry": "toroid" | "rod" | "slug" | "bobbin",
  "dimensions": { ... geometry-dependent ... },
  "material": {
    "name": "Custom Fair-Rite 78",
    "mu_r_initial": 2300,
    "mu_curve": null,
    "b_sat_t": 0.42,
    "loss_factor_at_freq": { "freq_hz": 100000, "tan_delta": 0.005 },
    "freq_range_hz": { "min": 10000, "max": 1000000 }
  }
}
```

| Field                          | Type           | Notes                                                                                                                      |
| ------------------------------ | -------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `geometry`                     | enum           | Determines which `dimensions` keys are required                                                                            |
| `dimensions`                   | object         | See §5.3                                                                                                                   |
| `material.mu_r_initial`        | number         | Bulk relative permeability at low signal level, low frequency                                                              |
| `material.mu_curve`            | nullable array | Optional `[{freq_hz, mu_r_real, mu_r_imag}, ...]` — when present, the solver interpolates μ(f) instead of using the scalar |
| `material.b_sat_t`             | number         | Saturation flux density in tesla. Triggers a warning when the operating B is within 30% of B_sat.                          |
| `material.loss_factor_at_freq` | object         | tan δ at a reference frequency. Used to estimate core loss contribution to Q.                                              |
| `material.freq_range_hz`       | object         | Soft validity range. Outside this range emits a `frequency_out_of_range` warning, not an error.                            |

**v1 limitation — μ vs DC-bias not modelled.** The kernel treats `μ_r` as
independent of the DC operating point. This is acceptable for amateur HF
inductors and matching-network use, but underestimates inductance roll-off
in heavily-biased EMI chokes and switching-converter cores. The frontend
UI surfaces this caveat near the toroid mode; the schema deliberately does
not carry a DC-bias field in v1.

### 5.3 Dimensions by geometry

**Toroid:**
```json
{ "od_m": 0.0095, "id_m": 0.0053, "h_m": 0.0033 }
```
Effective values used by the solver:
- `A_e = h·(od−id)/2` (effective cross-section)
- `l_e = π·(od+id)/2` (effective magnetic path length)
- `A_L = μ_0·μ_r·A_e/l_e` (nH per turn² × 10⁹)

**Rod / slug (cylindrical):**
```json
{ "diameter_m": 0.006, "length_m": 0.020 }
```
Used inside solenoids — the open-circuit permeability `μ_rod` is
substantially lower than bulk `μ_r` for short rods; the kernel applies the
Brookes/Watt correction.

**Bobbin (E-core simplification):**
```json
{ "a_e_m2": 1.2e-5, "l_e_m": 0.040 }
```
Frontend lets the user enter A_e and l_e directly when a manufacturer
datasheet quotes them.

---

## 6. Response envelope

```json
{
  "mode": "solenoid",
  "inductance_h": 5.6e-6,
  "dc_resistance_ohm": 0.18,
  "q_at_frequency": 142.0,
  "srf_hz": 48000000.0,
  "details": { ... mode-specific ... },
  "warnings": [
    { "code": "near_saturation", "message": "Operating B = 0.31 T (74% of B_sat)" }
  ]
}
```

| Field               | Always present                                   | Notes                                                                               |
| ------------------- | ------------------------------------------------ | ----------------------------------------------------------------------------------- |
| `mode`              | yes                                              | Echoes the request                                                                  |
| `inductance_h`      | yes                                              | Self-inductance (primary self for `coupled`) in henries                             |
| `dc_resistance_ohm` | yes                                              | Total DC resistance of all windings                                                 |
| `q_at_frequency`    | yes for solenoid/toroid/spiral; null for coupled | Q at `frequency_hz`                                                                 |
| `srf_hz`            | when computable                                  | Self-resonant frequency. `null` if the model can't estimate it (e.g. some toroids). |
| `details`           | yes                                              | Mode-specific extras                                                                |
| `warnings`          | always (possibly empty array)                    | Soft conditions; never an error                                                     |

### 6.1 Mode-specific `details`

**solenoid:**
```json
{
  "wire_length_m": 0.785,
  "wire_diameter_m": 0.0005,
  "effective_permeability": 1.0,
  "skin_depth_m": 0.000024,
  "ac_resistance_ohm": 0.45,
  "stored_energy_j": 2.8e-9,
  "operating_b_t": 0.0
}
```

**toroid:**
```json
{
  "wire_length_m": 0.42,
  "al_nh_per_n2": 4.0,
  "effective_permeability": 10.0,
  "core_loss_w": 0.0012,
  "operating_b_t": 0.085,
  "fill_fraction": 0.62
}
```

**spiral:**
```json
{
  "trace_length_m": 0.094,
  "k1": 2.34, "k2": 2.75,
  "fill_ratio": 0.43,
  "parasitic_capacitance_f": 1.8e-13,
  "q_conductor": 38.0,
  "q_dielectric": 220.0,
  "current_capacity_a": 0.6
}
```
`q_at_frequency` in the top-level response is the combined value:
`1/Q_total = 1/q_conductor + 1/q_dielectric`. The two components are
echoed so the frontend can show users where the loss is coming from.

**coupled:**
```json
{
  "primary":   { ... full solenoid/toroid details ... },
  "secondary": { ... full solenoid/toroid details ... },
  "mutual_inductance_h": 4.7e-6,
  "coupling_k": 0.92,
  "leakage_inductance_primary_h": 0.45e-6,
  "leakage_inductance_secondary_h": 0.42e-6,
  "turns_ratio": 2.0,
  "impedance_ratio": 4.0
}
```

### 6.2 Warning codes

| Code                         | Trigger                                                                        |
| ---------------------------- | ------------------------------------------------------------------------------ |
| `near_saturation`            | Operating B ≥ 70% of `b_sat_t`                                                 |
| `above_srf`                  | `frequency_hz > srf_hz`                                                        |
| `frequency_out_of_range`     | `frequency_hz` outside `freq_range_hz` of the core                             |
| `window_fill_exceeded`       | Toroid: wire won't physically fit in the window                                |
| `thin_wire_marginal`         | Wire AWG < 30 for currents implied by stored energy                            |
| `coupling_geometry_estimate` | `coupling_k` was geometrically estimated (not measured) — accuracy ±15%        |
| `fractional_toroid_turns`    | Non-integer `turns` on a toroid mode (rejected as error if outside [0.5, 100]) |

---

## 7. Bundled core library

Stored as `data/cores.json` in this repo. Antenna Studio reads its own copy
(initially identical; drift checked at build time via a `make verify-cores`
target — see §10). The file is an array of `CoreSpec` objects with a
`preset_id` field added at the top level:

```json
[
  {
    "preset_id": "T-37-2",
    "name": "Amidon T-37-2",
    "family": "iron_powder",
    "color_code": "red",
    "geometry": "toroid",
    "dimensions": { "od_m": 0.0095, "id_m": 0.0053, "h_m": 0.0033 },
    "material": {
      "name": "Carbonyl E (Mix 2)",
      "mu_r_initial": 10,
      "b_sat_t": 0.5,
      "loss_factor_at_freq": { "freq_hz": 7000000, "tan_delta": 0.012 },
      "freq_range_hz": { "min": 1000000, "max": 30000000 }
    },
    "al_nh_per_n2_override": 4.0
  },
  ...
]
```

The optional `al_nh_per_n2_override` lets the catalog use the manufacturer's
published AL when it differs from the geometry-derived value (commonly does
for iron-powder cores). When absent, the kernel computes A_L from the
dimensions + μ_r.

**Initial catalog** (v1):

| preset_id                                       | family                 | freq range   | use                           |
| ----------------------------------------------- | ---------------------- | ------------ | ----------------------------- |
| T-37-2, T-50-2, T-68-2, T-80-2, T-94-2, T-106-2 | iron_powder (red)      | 1–30 MHz     | HF inductors, matching        |
| T-37-6, T-50-6, T-68-6, T-80-6                  | iron_powder (yellow)   | 10–50 MHz    | VHF lower band                |
| T-37-10, T-50-10                                | iron_powder (black)    | 30–100 MHz   | VHF                           |
| FT-37-43, FT-50-43, FT-82-43, FT-114-43         | ferrite (Fair-Rite 43) | 1–10 MHz     | RF chokes, EMI, baluns        |
| FT-37-61, FT-50-61, FT-82-61                    | ferrite (61)           | 10–200 MHz   | VHF/UHF baluns, chokes        |
| FT-37-77, FT-50-77                              | ferrite (77)           | 10 kHz–1 MHz | switching, audio transformers |
| BN-43-202, BN-43-2402, BN-43-2402               | binocular ferrite      | 1–300 MHz    | broadband transformers        |

(Final IDs and counts firm up when we write `data/cores.json`. About
20–25 presets is the v1 target.)

---

## 8. Endpoint

```
POST /api/inductor/design
Content-Type: application/json
```

**Success:** 200 with the response envelope (§6).

**Errors:** 400 with:
```json
{
  "error_code": "core.not_found" | "validation.field" | "validation.coupled_mismatch" | ...,
  "field": "params.core.id",
  "message": "Core preset 'T-37-99' not found in catalog"
}
```

A separate read-only endpoint exposes the catalog so the frontend can render
preset pickers without hard-coding the list:

```
GET /api/inductor/cores
```

Response:
```json
{ "cores": [ CoreSpec, ... ] }
```

The frontend caches this list once per session.

---

## 9. Validation summary

The kernel must reject (400) on:
- `mode` not one of the four
- `frequency_hz <= 0`
- Any geometry dimension `<= 0`
- Toroid with `core = null` or `core.geometry != "toroid"`
- Spiral with `outer_diameter_m <= inner_diameter_m`
- Spiral where `(outer − inner)/2 < turns·(trace_width + trace_spacing)` (won't fit)
- Coupled with `shared_core = true` and `primary.mode != secondary.mode`
- Coupled with `coupling_k_override` not in (0, 1]
- Wire with neither `diameter_m` nor `awg`

The kernel must warn (response.warnings) on:
- The seven codes in §6.2

Anything in between (e.g. unusual but physical geometries) goes through.

---

## 10. Cross-repo drift protection

Antenna Studio keeps a verbatim copy of `data/cores.json`. A Makefile
target in each repo (`make verify-cores`) computes a SHA-256 of the JSON and
compares it to a checked-in `data/cores.json.sha256`. CI fails on mismatch.
When the catalog is updated, regenerate both checksum files and bump them
in the same PR pair.

The Go kernel itself (`pkg/inductor/`) is **not** checksum-protected — the
mode types and the math are stable enough that lockstep updates are
acceptable. When a real divergence pain point appears, the kernel graduates
to a third Go module with a `replace` directive. Not before.

---

## 11. v1 decisions log

These were open questions during spec drafting and have been settled in the
schema above. Captured here so a future reader sees the reasoning behind a
spec choice that might otherwise look arbitrary.

1. **Spiral substrate dielectric loss is modelled.** `substrate.tan_delta`
   feeds a `Q_dielectric` term; `Q_total` combines conductor and dielectric
   contributions (see §3.3 and §6.1). Frontend supplies the values via
   laminate presets; the wire format carries raw numbers only.
2. **Litz wire deferred to v2.** v1 is solid round magnet wire only (see §4).
3. **μ vs DC-bias not modelled in v1.** Acceptable for HF / matching use;
   under-models EMI-choke saturation behaviour. Limitation surfaced in the
   UI (see §5.2).
4. **AWG table is a compile-time Go literal.** The Brown & Sharpe diameters
   are mathematically frozen (formula in §4); ASTM B258 revisions are
   editorial. User-extension path is `wire.diameter_m` directly — no
   hot-reload plumbing.
5. **Coupled-coil k stays geometry-estimated for non-shared cores**, with the
   `coupling_geometry_estimate` warning recommending `coupling_k_override`
   for serious work (see §3.4, §6.2).
