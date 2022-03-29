package docker

import (
	"context"
	"fmt"
)

type ImageDiskLoader interface {
	LoadFromFile(ctx context.Context, filepath string) error
}

type ImageDiskWriter interface {
	SaveToFile(ctx context.Context, filepath string, images ...string) error
}

type ImagePusher interface {
	PushImage(ctx context.Context, image string, endpoint string) error
}

type ImagePuller interface {
	PullImage(ctx context.Context, image string) error
}

type DockerClient interface {
	ImageDiskLoader
	ImageDiskWriter
	ImagePuller
	ImagePusher
}

// ImageSource represents a generic source for container images that can be loaded
// into the local docker cache
type ImageSource interface {
	Load(ctx context.Context, images ...string) error
}

// ImageDestination represents a generic destination for container images that
// can be written from the local docker cache
type ImageDestination interface {
	Write(ctx context.Context, images ...string) error
}

// ImageMover orchestrates loading images from a source and writing them to a destination
type ImageMover struct {
	source      ImageSource
	destination ImageDestination
}

func NewImageMover(source ImageSource, destination ImageDestination) *ImageMover {
	return &ImageMover{
		source:      source,
		destination: destination,
	}
}

// Move loads images from source and writes them to the destination
func (m *ImageMover) Move(ctx context.Context, images ...string) error {
	if err := m.source.Load(ctx, images...); err != nil {
		return fmt.Errorf("loading docker image mover source: %v", err)
	}

	if err := m.destination.Write(ctx, images...); err != nil {
		return fmt.Errorf("writing images to destination with image mover: %v", err)
	}

	return nil
}
