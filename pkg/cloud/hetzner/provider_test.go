package hetzner

import (
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/opencost/opencost/pkg/config"
	v1 "k8s.io/api/core/v1"
)

func TestGetKeyPreservesProviderID(t *testing.T) {
	h := &Hetzner{}
	labels := map[string]string{
		v1.LabelTopologyRegion:     "fsn1",
		v1.LabelInstanceTypeStable: "cpx31",
	}

	key := h.GetKey(labels, &clustercache.Node{
		SpecProviderID: "hcloud://123456",
		Labels:         labels,
	})

	if got := key.ID(); got != "hcloud://123456" {
		t.Errorf("ID() = %q, want %q", got, "hcloud://123456")
	}
	if got := key.Features(); got != "fsn1,cpx31" {
		t.Errorf("Features() = %q, want %q", got, "fsn1,cpx31")
	}
}

func TestClusterInfo(t *testing.T) {
	h := &Hetzner{
		Config:           testProviderConfig{},
		ClusterRegion:    "hel1",
		ClusterAccountID: "project-a",
	}

	info, err := h.ClusterInfo()
	if err != nil {
		t.Fatalf("ClusterInfo returned error: %v", err)
	}

	if info["provider"] != opencost.HetznerProvider {
		t.Errorf("provider = %q, want %q", info["provider"], opencost.HetznerProvider)
	}
	if info["region"] != "hel1" {
		t.Errorf("region = %q, want %q", info["region"], "hel1")
	}
	if info["account"] != "project-a" {
		t.Errorf("account = %q, want %q", info["account"], "project-a")
	}
}

type testProviderConfig struct{}

func (testProviderConfig) ConfigFileManager() *config.ConfigFileManager {
	return nil
}

func (testProviderConfig) GetCustomPricingData() (*models.CustomPricing, error) {
	return &models.CustomPricing{}, nil
}

func (testProviderConfig) Update(func(*models.CustomPricing) error) (*models.CustomPricing, error) {
	return &models.CustomPricing{}, nil
}

func (testProviderConfig) UpdateFromMap(map[string]string) (*models.CustomPricing, error) {
	return &models.CustomPricing{}, nil
}
