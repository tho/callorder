// Package initcaller treats init as an ordinary caller by default.
package initcaller

var registry = map[string]func(){}

// Run runs the registered function.
func Run() { registry["run"]() }

func register() { registry["run"] = Run } // want `expected init before register in depth-first call order`

func init() { register() }
