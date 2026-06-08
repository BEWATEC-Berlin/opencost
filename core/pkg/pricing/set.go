package pricing

import (
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"

	"github.com/opencost/opencost/core/pkg/unit"
)

type PricingSet struct {
	Nodes   []*NodePricing   `json:"nodes" yaml:"nodes"`
	Volumes []*VolumePricing `json:"volumes" yaml:"volumes"`
}

func (ps *PricingSet) IsEmpty() bool {
	if ps == nil {
		return true
	}

	return len(ps.Nodes) == 0 && len(ps.Volumes) == 0
}

func (ps *PricingSet) Checksum() (string, error) {
	slices.SortFunc(ps.Nodes, func(npa, npb *NodePricing) int {
		return strings.Compare(npa.String(), npb.String())
	})

	builder := strings.Builder{}

	for _, np := range ps.Nodes {
		_, err := builder.WriteString(np.String())
		if err != nil {
			return "", fmt.Errorf("building string: %w", err)
		}
	}

	hasher := fnv.New64a()
	_, err := hasher.Write([]byte(builder.String()))
	if err != nil {
		return "", fmt.Errorf("fnv hash: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (ps *PricingSet) Currencies() []unit.Currency {
	if ps == nil {
		return []unit.Currency{}
	}

	currencies := map[unit.Currency]struct{}{}

	for _, np := range ps.Nodes {
		for _, curr := range np.GetCurrencies() {
			currencies[curr] = struct{}{}
		}
	}

	for _, vp := range ps.Volumes {
		for _, curr := range vp.GetCurrencies() {
			currencies[curr] = struct{}{}
		}
	}

	return slices.Collect(maps.Keys(currencies))
}
