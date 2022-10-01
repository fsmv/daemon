package tools

// Use this to define a flag that has a callback like with [flag.Func] but label
// it as a boolean flag to the go flag parser. Use [flag.Func] for non-bool
// flags.
//
// This means you can invoke the flag by -foobar instead of -foobar=true, and
// the callback will be called by [flag.Parse].
//
// Ex: func init() { flag.Var(tools.BoolFuncFlag(myCallback), "name", "usage") }
type BoolFuncFlag func(string) error

func (b BoolFuncFlag) String() string {
	return ""
}

func (b BoolFuncFlag) Set(s string) error {
	return b(s)
}

// Returns true. This is why you need a helper type and can't just use flag.Var
// to get this behavior.
func (b BoolFuncFlag) IsBoolFlag() bool {
	return true
}
