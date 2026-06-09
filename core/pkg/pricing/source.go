package pricing

import (
	"context"

	"github.com/opencost/opencost/core/pkg/reader"
)

type PricingSource interface {
	NodePricingSource
	VolumePricingSource
	GetPricingSet(context.Context) (*PricingSet, error)
	SourceName() string
}

// TODO: add the following function for Opencost pricing
// GetNodePricing(NodePricingProperties) (*NodePricing, error)
type NodePricingSource interface {
	NewNodePricingReader(ctx context.Context) (reader.Reader[*NodePricing], error)
}

// TODO: add the following function for Opencost pricing
// GetVolumePricing(VolumePricingProperties) (*VolumePricing, error)
type VolumePricingSource interface {
	NewVolumePricingReader(ctx context.Context) (reader.Reader[*VolumePricing], error)
}
