package store

import "vivarium/internal/models"

// Stable IDs for the bundled standard base images so reseeding is idempotent.
const (
	StandardOpenCodeImageID = "00000000-0000-4000-8000-000000000001"
	StandardPiImageID       = "00000000-0000-4000-8000-000000000002"
)

// StandardBaseImages returns the two bundled base image presets.
//
// Note: DATA_MODELS.md refers to "four" pre-bundled images but only lists two;
// the listed presets are seeded here.
func StandardBaseImages() []models.BaseImage {
	return []models.BaseImage{
		{
			ID:               StandardOpenCodeImageID,
			Name:             "Ubuntu 24.04 LTS + OpenCode",
			SourceType:       models.SourceStandard,
			SourcePathOrRepo: "vivarium/ubuntu-24.04-opencode:latest",
			DefaultUser:      "root",
			IsInternal:       true,
		},
		{
			ID:               StandardPiImageID,
			Name:             "Ubuntu 24.04 LTS + Pi",
			SourceType:       models.SourceStandard,
			SourcePathOrRepo: "vivarium/ubuntu-24.04-pi:latest",
			DefaultUser:      "root",
			IsInternal:       true,
		},
	}
}
