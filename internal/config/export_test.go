package config

// SetLookPath swaps the binary lookup and returns a function that restores it.
func SetLookPath(f func(string) (string, error)) (restore func()) {
	old := lookPath
	lookPath = f
	return func() { lookPath = old }
}
