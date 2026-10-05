// Package parity is the shared machinery of an extraction's parity proof.
//
// An extraction moves objects from a template of the consuming repository into a chart of a public
// repository. Each move is held to the same claims, and used to rebuild the
// same helpers in its own tests:
//
//   - the chart, fed the values the repository computes for it, renders exactly the
//     objects the stack rendered before (a frozen fixture), field by field;
//   - the two sources of the Application are disjoint and together equal that
//     frozen render (a swap);
//   - the values the Application carries equal an independent Go computation;
//   - every object a swap moves is guarded against the prune window (Move,
//     GuardMoves).
//
// This package holds what is common: decoding a render into keyed objects
// (Decode), comparing object sets with clear diffs (AssertSame,
// AssertDisjoint, Union), rendering a vendored or live chart (ChartPath,
// Template), frozen fixtures (Fixtures), and the table of moves (Move) that the swap guard and the parity
// tests consume. An extraction writes only its value mapping and one row.
//
// The package imports no test-only code of its callers: everything takes a
// testing.TB.
package parity
