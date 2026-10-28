//go:build !unix

package update

func lock(string) (func(), error) { return func() {}, nil }
