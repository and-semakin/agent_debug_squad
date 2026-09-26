//go:build !unix

package preflight

func syscallAccessOS(path string) error {
	return nil
}
