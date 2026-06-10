package pricing

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencost/opencost/core/pkg/reader"
	"gopkg.in/yaml.v3"
)

type MockPricingModule struct {
	NodePricing   []*NodePricing
	VolumePricing []*PersistentVolumePricing
}

func NewMockPricingModule() (*MockPricingModule, error) {
	mpm := &MockPricingModule{
		NodePricing:   []*NodePricing{},
		VolumePricing: []*PersistentVolumePricing{},
	}

	// Default
	defaultPricingSet, err := loadTestFile("default.yaml")
	if err != nil {
		return nil, fmt.Errorf("error loading test default pricing: %w", err)
	}
	mpm.NodePricing = append(mpm.NodePricing, defaultPricingSet.Nodes...)
	mpm.VolumePricing = append(mpm.VolumePricing, defaultPricingSet.Volumes...)

	// AWS
	awsPricingSet, err := loadTestFile("aws.yaml")
	if err != nil {
		return nil, fmt.Errorf("error loading test AWS pricing: %w", err)
	}
	mpm.NodePricing = append(mpm.NodePricing, awsPricingSet.Nodes...)
	mpm.VolumePricing = append(mpm.VolumePricing, awsPricingSet.Volumes...)

	// Azure
	azurePricingSet, err := loadTestFile("azure.yaml")
	if err != nil {
		return nil, fmt.Errorf("error loading test Azure pricing: %w", err)
	}
	mpm.NodePricing = append(mpm.NodePricing, azurePricingSet.Nodes...)
	mpm.VolumePricing = append(mpm.VolumePricing, azurePricingSet.Volumes...)

	// GCP
	gcpPricingSet, err := loadTestFile("gcp.yaml")
	if err != nil {
		return nil, fmt.Errorf("error loading test GCP pricing: %w", err)
	}
	mpm.NodePricing = append(mpm.NodePricing, gcpPricingSet.Nodes...)
	mpm.VolumePricing = append(mpm.VolumePricing, gcpPricingSet.Volumes...)

	return mpm, nil
}

func (mpm *MockPricingModule) Checksum(ctx context.Context) (string, error) {
	ps, err := mpm.GetPricingSet(ctx)
	if err != nil {
		return "", fmt.Errorf("getting pricing set: %w", err)
	}

	return ps.Checksum()
}

func (mpm *MockPricingModule) GetPricingSet(ctx context.Context) (*PricingSet, error) {
	ps := &PricingSet{
		Nodes:   mpm.NodePricing,
		Volumes: mpm.VolumePricing,
	}

	return ps, nil
}

func (mpm *MockPricingModule) SourceName() string {
	return "mock"
}

func (mpm *MockPricingModule) NewNodePricingReader(ctx context.Context) (reader.Reader[*NodePricing], error) {
	return reader.NewSliceReader(mpm.NodePricing), nil
}

func (mpm *MockPricingModule) NewPersistentVolumePricingReader(ctx context.Context) (reader.Reader[*PersistentVolumePricing], error) {
	return reader.NewSliceReader(mpm.VolumePricing), nil
}

//go:embed test/*
var pricingTestFS embed.FS

func loadTestFile(filename string) (*PricingSet, error) {
	path := filepath.Join("test", filename)
	bs, err := pricingTestFS.ReadFile(path)
	if err != nil {
		panic(fmt.Errorf("failed to read embedded pricing file: %w", err))
	}

	var set *PricingSet

	// Detect file format based on extension
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json":
		err = json.Unmarshal(bs, &set)
		if err != nil {
			return nil, fmt.Errorf("failed to parse json: %w", err)
		}
	case ".yaml", ".yml":
		err = yaml.Unmarshal(bs, &set)
		if err != nil {
			return nil, fmt.Errorf("failed to parse yaml: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported file format: %s (expected .json, .yaml, or .yml)", ext)
	}

	return set, nil
}
