package upgrade

import (
	"debug/elf"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	if expected := os.Getenv("ANDEY_SIGNED_VERSION"); expected != "" && manifest.Version != expected {
		t.Fatalf("version %s differs from release %s", manifest.Version, expected)
	}
	f, err := elf.Open(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Fatal("release binary requires an external dynamic loader")
		}
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == manifest.GOARCH {
		output, err := exec.Command(binaryPath, "-v").CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) != manifest.Version {
			t.Fatalf("executable version mismatch: %s, %v", output, err)
		}
	}
}
