package calls

// Calls across files impose no order, so this file is already in order.

// Report is an entry point.
func Report() string { return helperFromA() }

func helperFromA() string { return shared() + Load() }
