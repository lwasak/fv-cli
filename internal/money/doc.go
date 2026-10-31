// Package money holds locale-agnostic amount parsing and VAT arithmetic.
//
// Amounts are integer grosze (Amount). Nothing in this package reads the
// environment or the OS locale, and no floating-point type is used; tests
// enforce both.
package money
