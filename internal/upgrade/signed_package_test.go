package upgrade

import (
	"os"
	"testing"
)

// This integration test is enabled by the release verification job after a
// signed package has been built. Ordinary unit-test runs skip it.
func TestBuiltSignedPackage(t *testing.T) {
	path := os.Getenv("ANDEY_SIGNED_RUN")
	if path == "" {
		t.Skip("ANDEY_SIGNED_RUN is not set")
	}
	manifest, binaryPath, err := inspectSignedRun(path, t.TempDir())
	if err != nil {
		t.Fatalf("inspect signed package: %v", err)
	}
	defer os.Remove(binaryPath)
	expectedArch := os.Getenv("ANDEY_SIGNED_ARCH")
	if !versionPattern.MatchString(manifest.Version) || manifest.GOOS != "linux" || (expectedArch != "" && manifest.GOARCH != expectedArch) {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
}
