package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGPUEnumerate(t *testing.T) {
	root := t.TempDir()
	drmRoot := filepath.Join(root, "class", "drm")
	pciRoot := filepath.Join(root, "bus", "pci", "devices")
	if err := os.MkdirAll(drmRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// AMD render node.
	amdPCI := filepath.Join(pciRoot, "0000:03:00.0")
	if err := os.MkdirAll(amdPCI, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(amdPCI, "vendor"), []byte("0x1002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	amdNode := filepath.Join(drmRoot, "renderD128")
	if err := os.MkdirAll(amdNode, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(amdPCI, filepath.Join(amdNode, "device")); err != nil {
		t.Fatal(err)
	}

	// Non-AMD render node that must be ignored.
	intelPCI := filepath.Join(pciRoot, "0000:00:02.0")
	if err := os.MkdirAll(intelPCI, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(intelPCI, "vendor"), []byte("0x8086\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	intelNode := filepath.Join(drmRoot, "renderD129")
	if err := os.MkdirAll(intelNode, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(intelPCI, filepath.Join(intelNode, "device")); err != nil {
		t.Fatal(err)
	}

	enum := GPUEnumerator{
		DrmRoot: drmRoot,
		Lspci:   func(bdf string) (string, error) { return "AMD Radeon Pro / AI Series", nil },
	}
	gpus, err := enum.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 {
		t.Fatalf("expected 1 AMD GPU, got %d: %+v", len(gpus), gpus)
	}
	if gpus[0].DeviceID != "0000:03:00.0" {
		t.Fatalf("device id = %q", gpus[0].DeviceID)
	}
	if gpus[0].RenderNode != "/dev/dri/renderD128" {
		t.Fatalf("render node = %q", gpus[0].RenderNode)
	}
	if gpus[0].Name != "AMD Radeon Pro / AI Series" {
		t.Fatalf("name = %q", gpus[0].Name)
	}
}

func TestParseLspciName(t *testing.T) {
	line := `0000:03:00.0 "VGA compatible controller" "Advanced Micro Devices, Inc. [AMD/ATI]" "Radeon Pro" -r00`
	if got := parseLspciName(line); got != "Advanced Micro Devices, Inc. [AMD/ATI] Radeon Pro" {
		t.Fatalf("parseLspciName = %q", got)
	}
}
