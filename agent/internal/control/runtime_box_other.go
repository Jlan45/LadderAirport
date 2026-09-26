//go:build !android && !with_android

package control

// sanitizePlatformConfig for non-Android platforms is an inlined no-op.
func sanitizePlatformConfig(configJSON string) (string, error) {
	return configJSON, nil
}
