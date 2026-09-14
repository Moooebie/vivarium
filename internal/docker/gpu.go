package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"vivarium/internal/models"
)

// amdVendorID is the PCI vendor identifier for AMD.
const amdVendorID = "0x1002"

var renderNodePattern = regexp.MustCompile(`^renderD[0-9]+$`)

// GPUEnumerator discovers AMD GPUs by scanning sysfs.
type GPUEnumerator struct {
	// DrmRoot is the DRM class directory (default /sys/class/drm).
	DrmRoot string
	// Lspci resolves a human-readable name for a PCI address. It may be nil,
	// in which case a generic name is used.
	Lspci func(bdf string) (string, error)
}

// DefaultGPUEnumerator returns an enumerator rooted at the real sysfs paths.
func DefaultGPUEnumerator() GPUEnumerator {
	return GPUEnumerator{
		DrmRoot: "/sys/class/drm",
		Lspci:   lspciName,
	}
}

// Enumerate returns the AMD GPUs present on the host.
func (e GPUEnumerator) Enumerate() ([]models.GPU, error) {
	drmRoot := e.DrmRoot
	if drmRoot == "" {
		drmRoot = "/sys/class/drm"
	}
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil, err
	}
	var gpus []models.GPU
	for _, ent := range entries {
		name := ent.Name()
		if !renderNodePattern.MatchString(name) {
			continue
		}
		devLink := filepath.Join(drmRoot, name, "device")
		vendorRaw, err := os.ReadFile(filepath.Join(devLink, "vendor"))
		if err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(string(vendorRaw)), amdVendorID) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(devLink)
		if err != nil {
			continue
		}
		bdf := filepath.Base(resolved)
		gpuName := "AMD GPU " + bdf
		if e.Lspci != nil {
			if resolvedName, err := e.Lspci(bdf); err == nil && resolvedName != "" {
				gpuName = resolvedName
			}
		}
		gpus = append(gpus, models.GPU{
			DeviceID:   bdf,
			Name:       gpuName,
			RenderNode: filepath.Join("/dev/dri", name),
		})
	}
	sort.Slice(gpus, func(i, j int) bool { return gpus[i].RenderNode < gpus[j].RenderNode })
	return gpus, nil
}

// lspciName resolves a PCI address to a "vendor device" string using lspci.
func lspciName(bdf string) (string, error) {
	out, err := exec.Command("lspci", "-s", bdf, "-mm").Output()
	if err != nil {
		return "", err
	}
	return parseLspciName(string(out)), nil
}

// parseLspciName extracts the vendor and device fields from lspci -mm output.
func parseLspciName(out string) string {
	fields := quotedFields(out)
	if len(fields) < 4 {
		return ""
	}
	name := strings.TrimSpace(fields[2] + " " + fields[3])
	return name
}

// quotedFields splits a line into quoted fields, honouring double quotes.
func quotedFields(s string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '"':
			if inQuote {
				flush()
			}
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return fields
}
