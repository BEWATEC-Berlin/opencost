package pricing

import (
	"fmt"
	"strings"
)

type Resource string

const (
	ResourceNil     Resource = ""
	ResourceNode    Resource = "node"
	ResourceCPU     Resource = "cpu"
	ResourceRAM     Resource = "ram"
	ResourceGPU     Resource = "gpu"
	ResourceStorage Resource = "storage"
)

func ParseResource(str string) (Resource, error) {
	switch strings.ToLower(str) {
	case string(ResourceNode):
		return ResourceNode, nil
	case string(ResourceCPU):
		return ResourceCPU, nil
	case string(ResourceRAM):
		return ResourceRAM, nil
	case string(ResourceGPU):
		return ResourceGPU, nil
	case string(ResourceStorage):
		return ResourceStorage, nil
	default:
		return ResourceNil, fmt.Errorf("unknown resource %q", str)
	}
}
