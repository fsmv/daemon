package tools

// Use this to define a flag that has a callback like with flag.Func but label
// it as a boolean flag to the go flag parser. Use flag.Func for non-bool flags.
//
// Main usecase is to define flags in this tools library that have a callback
// function when flag.Parse() is called.
//
// Ex: flag.Var(tools.BoolFlagFunc(myCallback), "name", "usage")
type BoolFunc func(string) error

func (b BoolFunc) String() string {
  return ""
}

func (b BoolFunc) Set(s string) error {
  return b(s)
}

func (b BoolFunc) IsBoolFlag() bool {
  return true
}
