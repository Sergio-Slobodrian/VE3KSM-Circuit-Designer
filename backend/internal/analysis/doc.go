// Package analysis turns raw simulation output (time-domain samples,
// AC sweep complex values) into the derived measurements the UI shows:
// THD, THD+N, SNR, SINAD, group delay, -3 dB cutoff, peak find, marker
// math, harmonic-table extraction, FFT post-processing.
//
// Use ngspice's built-in `spec` and `fft` commands where possible — see
// DESIGN.md §10.4. This package is for what the engine does not provide
// natively (auto-marker searches, harmonic phase extraction, etc.).
package analysis
