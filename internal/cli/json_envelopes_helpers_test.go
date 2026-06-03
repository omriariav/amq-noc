package cli

import "os"

// writeOrFail is a small wrapper so json-envelope tests can write a body to
// a path without inlining os.WriteFile boilerplate. Permissions are 0o644.
func writeOrFail(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
