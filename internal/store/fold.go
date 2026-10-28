package store

import (
	"database/sql/driver"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// fold is the search form of a string: NFC-normalised and case-folded with full Unicode folding, so "ПРИВІТ"
// matches "привіт" and a composed é matches a decomposed one, while accents still count. Changing it needs a
// migration that recomputes every stored _fold column.
func fold(s string) string {
	return cases.Fold().String(norm.NFC.String(s))
}

var registerFoldOnce sync.Once

// registerFold makes fold available to SQL as mcp_fold(text), for the device store's contact names, which have
// no folded column. It must run before the first connection is opened: modernc applies registered functions only
// to connections opened afterwards.
func registerFold() {
	registerFoldOnce.Do(func() {
		err := sqlite.RegisterDeterministicScalarFunction("mcp_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return fold(v), nil
			case []byte:
				return fold(string(v)), nil
			default:
				return v, nil
			}
		})
		if err != nil {
			panic("store: registering mcp_fold: " + err.Error())
		}
	})
}
