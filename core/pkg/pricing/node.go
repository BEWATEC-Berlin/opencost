package pricing

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/opencost/opencost/core/pkg/unit"
)

type NodePricing struct {
	Properties NodePricingProperties `json:"properties" yaml:"properties"`
	Prices     Prices                `json:"prices" yaml:"pricing"`
}

func (np *NodePricing) GetCurrencies() []unit.Currency {
	currencies := map[unit.Currency]struct{}{}

	for currency := range np.Prices {
		currencies[currency] = struct{}{}
	}

	return slices.Collect(maps.Keys(currencies))
}

// TODO this actually has to have price too.... ugh
func (np *NodePricing) String() string {
	return np.Properties.String()
}

type NodePricingProperties struct {
	Provider     Provider          `json:"provider,omitempty" yaml:"provider,omitempty"`
	Region       string            `json:"region,omitempty" yaml:"region,omitempty"`
	InstanceType string            `json:"instanceType,omitempty" yaml:"instanceType,omitempty"`
	Provisioning ProvisioningType  `json:"provisioning,omitempty" yaml:"provisioning,omitempty"`
	Commitment   CommitmentType    `json:"commitment,omitempty" yaml:"commitment,omitempty"`
	Cluster      string            `json:"cluster,omitempty" yaml:"cluster,omitempty"`
	ProviderID   string            `json:"providerID,omitempty" yaml:"providerID,omitempty"`
	Labels       map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Start        *time.Time        `json:"start,omitempty" yaml:"start,omitempty"`
	End          *time.Time        `json:"end,omitempty" yaml:"end,omitempty"`
}

// TODO: precompute this somewhere along the way?
func (np *NodePricingProperties) String() string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s:%s:%s",
		np.Provider,
		np.Region,
		np.InstanceType,
		np.Provisioning,
		np.Commitment,
		np.Cluster,
		np.ProviderID,
		np.labelsKey(),
		np.timeKey(),
	)
}

func (np *NodePricingProperties) labelsKey() string {
	if len(np.Labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(np.Labels))
	for k := range np.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(':')
		}

		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(np.Labels[k])
	}

	return b.String()
}

func (np *NodePricingProperties) timeKey() string {
	s := "nil"
	e := "nil"

	if np.Start != nil {
		s = np.Start.UTC().Format(time.RFC3339Nano)
	}

	if np.End != nil {
		e = np.End.UTC().Format(time.RFC3339Nano)
	}

	return fmt.Sprintf("%s:%s", s, e)
}
