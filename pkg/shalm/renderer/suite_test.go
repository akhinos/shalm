package renderer

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestRenderer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Renderer")
}
