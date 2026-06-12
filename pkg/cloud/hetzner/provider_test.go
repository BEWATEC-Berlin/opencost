package hetzner

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/opencost/opencost/pkg/config"
	v1 "k8s.io/api/core/v1"
)

const nodePricingTolerance = 0.000000001

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

func TestNodePricingProviderIDValidation(t *testing.T) {
	provider := testHetznerProviderWithPricingData("net", []HetznerServer{{
		Project:  "prod",
		ID:       123456,
		Name:     "worker-1",
		Type:     "cpx21",
		Location: "fsn1",
		VCPU:     3,
		RAMGiB:   4,
	}}, map[locationTypeKey]HetznerHourlyPrice{
		{Location: "fsn1", Type: "cpx21"}: {NetHourly: 0.012, GrossHourly: 0.0143},
	})

	tests := []struct {
		name            string
		providerID      string
		wantErrContains string
	}{
		{name: "valid hcloud provider ID", providerID: "hcloud://123456"},
		{name: "empty provider ID", providerID: "", wantErrContains: "empty provider ID"},
		{name: "malformed provider ID", providerID: "hetzner://123456", wantErrContains: "expected hcloud://"},
		{name: "empty hcloud server ID", providerID: "hcloud://", wantErrContains: "empty server ID"},
		{name: "non-numeric hcloud server ID", providerID: "hcloud://not-a-number", wantErrContains: "parse server ID"},
		{name: "unknown hcloud server ID", providerID: "hcloud://404", wantErrContains: "server ID 404 not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node, _, err := provider.NodePricing(&hetznerKey{ProviderID: tc.providerID})
			if tc.wantErrContains == "" {
				if err != nil {
					t.Fatalf("NodePricing() error = %v, want nil", err)
				}
				if node == nil {
					t.Fatal("NodePricing() node = nil, want node")
				}
				return
			}
			if err == nil {
				t.Fatalf("NodePricing() error = nil, want %q", tc.wantErrContains)
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("NodePricing() error = %q, want containing %q", err, tc.wantErrContains)
			}
			if node != nil {
				t.Fatalf("NodePricing() node = %#v, want nil on error", node)
			}
		})
	}
}

func TestNodePricingResolvesDuplicateServerIDsByConfiguredProject(t *testing.T) {
	provider := testHetznerProviderWithPricingData("net", []HetznerServer{
		{Project: "prod", ID: 100, Name: "prod-worker", Type: "cpx21", Location: "fsn1", VCPU: 3, RAMGiB: 4},
		{Project: "stage", ID: 100, Name: "stage-worker", Type: "cx22", Location: "nbg1", VCPU: 2, RAMGiB: 4},
	}, map[locationTypeKey]HetznerHourlyPrice{
		{Location: "fsn1", Type: "cpx21"}: {NetHourly: 0.012, GrossHourly: 0.0143},
		{Location: "nbg1", Type: "cx22"}:  {NetHourly: 0.0095, GrossHourly: 0.0113},
	})
	provider.ClusterAccountID = "stage"

	node, _, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://100"})
	if err != nil {
		t.Fatalf("NodePricing() error = %v, want nil", err)
	}
	if node.InstanceType != "cx22" {
		t.Fatalf("InstanceType = %q, want stage server type %q", node.InstanceType, "cx22")
	}
	if node.Region != "nbg1" {
		t.Fatalf("Region = %q, want stage location %q", node.Region, "nbg1")
	}
	if node.Cost != "0.0095" {
		t.Fatalf("Cost = %q, want stage cost %q", node.Cost, "0.0095")
	}
}

func TestNodePricingFailsAmbiguousDuplicateServerIDsWithoutConfiguredProject(t *testing.T) {
	provider := testHetznerProviderWithPricingData("net", []HetznerServer{
		{Project: "prod", ID: 100, Name: "prod-worker", Type: "cpx21", Location: "fsn1", VCPU: 3, RAMGiB: 4},
		{Project: "stage", ID: 100, Name: "stage-worker", Type: "cx22", Location: "nbg1", VCPU: 2, RAMGiB: 4},
	}, map[locationTypeKey]HetznerHourlyPrice{
		{Location: "fsn1", Type: "cpx21"}: {NetHourly: 0.012, GrossHourly: 0.0143},
		{Location: "nbg1", Type: "cx22"}:  {NetHourly: 0.0095, GrossHourly: 0.0113},
	})

	node, _, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://100"})
	if err == nil {
		t.Fatal("NodePricing() error = nil, want ambiguous server ID error")
	}
	if !strings.Contains(err.Error(), "matched multiple Hetzner projects") {
		t.Fatalf("NodePricing() error = %q, want ambiguous project message", err)
	}
	if node != nil {
		t.Fatalf("NodePricing() node = %#v, want nil on ambiguity", node)
	}
}

func TestNodePricingFailsForIncompleteCachedServerData(t *testing.T) {
	tests := []struct {
		name            string
		server          HetznerServer
		prices          map[locationTypeKey]HetznerHourlyPrice
		wantErrContains string
	}{
		{
			name:            "missing server type",
			server:          HetznerServer{Project: "prod", ID: 100, Location: "fsn1", VCPU: 3, RAMGiB: 4},
			prices:          map[locationTypeKey]HetznerHourlyPrice{{Location: "fsn1", Type: "cpx21"}: {NetHourly: 0.012}},
			wantErrContains: "missing server type",
		},
		{
			name:            "missing server location",
			server:          HetznerServer{Project: "prod", ID: 100, Type: "cpx21", VCPU: 3, RAMGiB: 4},
			prices:          map[locationTypeKey]HetznerHourlyPrice{{Location: "fsn1", Type: "cpx21"}: {NetHourly: 0.012}},
			wantErrContains: "missing server location",
		},
		{
			name:            "missing pricing",
			server:          HetznerServer{Project: "prod", ID: 100, Type: "cpx21", Location: "fsn1", VCPU: 3, RAMGiB: 4},
			prices:          map[locationTypeKey]HetznerHourlyPrice{},
			wantErrContains: "missing server price",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := testHetznerProviderWithPricingData("net", []HetznerServer{tc.server}, tc.prices)

			node, _, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://100"})
			if err == nil {
				t.Fatalf("NodePricing() error = nil, want %q", tc.wantErrContains)
			}
			if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("NodePricing() error = %q, want containing %q", err, tc.wantErrContains)
			}
			if node != nil {
				t.Fatalf("NodePricing() node = %#v, want nil on error", node)
			}
		})
	}
}

func TestNodePricingReturnsFormattedOpenCostNodeFieldsAndMetadata(t *testing.T) {
	provider := testHetznerProviderWithPricingData("net", []HetznerServer{{
		Project:  "prod",
		ID:       123456,
		Name:     "worker-1",
		Type:     "cx22",
		Location: "hel1",
		VCPU:     2,
		RAMGiB:   4,
	}}, map[locationTypeKey]HetznerHourlyPrice{
		{Location: "hel1", Type: "cx22"}: {NetHourly: 0.036, GrossHourly: 0.04284},
	})

	node, meta, err := provider.NodePricing(&hetznerKey{
		Labels: map[string]string{
			v1.LabelTopologyRegion:     "ignored-label-region",
			v1.LabelInstanceTypeStable: "ignored-label-type",
			v1.LabelArchStable:         "arm64",
		},
		ProviderID: "hcloud://123456",
	})
	if err != nil {
		t.Fatalf("NodePricing() error = %v", err)
	}

	assertNodeField(t, "Cost", node.Cost, "0.036")
	assertNodeField(t, "VCPU", node.VCPU, "2")
	assertNodeField(t, "RAM", node.RAM, "4")
	assertNodeField(t, "RAMBytes", node.RAMBytes, "4294967296")
	assertNodeField(t, "VCPUCost", node.VCPUCost, "0.009")
	assertNodeField(t, "RAMCost", node.RAMCost, "0.0045")
	assertNodeField(t, "InstanceType", node.InstanceType, "cx22")
	assertNodeField(t, "Region", node.Region, "hel1")
	assertNodeField(t, "ProviderID", node.ProviderID, "hcloud://123456")
	assertNodeField(t, "UsageType", node.UsageType, "hetzner-cloud")
	if node.PricingType != models.Api {
		t.Fatalf("PricingType = %q, want %q", node.PricingType, models.Api)
	}
	assertNodeField(t, "ArchType", node.ArchType, "arm64")
	assertNodeField(t, "metadata currency", meta.Currency, "EUR")
	if !strings.Contains(meta.Source, "Hetzner") || !strings.Contains(meta.Source, "net") {
		t.Fatalf("metadata source = %q, want Hetzner source with currency mode", meta.Source)
	}
}

func TestNodePricingUsesGrossCurrencyMode(t *testing.T) {
	provider := testHetznerProviderWithPricingData("gross", []HetznerServer{{
		Project:  "prod",
		ID:       123456,
		Name:     "worker-1",
		Type:     "cx22",
		Location: "hel1",
		VCPU:     2,
		RAMGiB:   4,
	}}, map[locationTypeKey]HetznerHourlyPrice{
		{Location: "hel1", Type: "cx22"}: {NetHourly: 0.036, GrossHourly: 0.04284},
	})

	node, meta, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://123456"})
	if err != nil {
		t.Fatalf("NodePricing() error = %v", err)
	}
	assertNodeField(t, "Cost", node.Cost, "0.04284")
	assertNodeField(t, "VCPUCost", node.VCPUCost, "0.01071")
	assertNodeField(t, "RAMCost", node.RAMCost, "0.005355")
	if !strings.Contains(meta.Source, "gross") {
		t.Fatalf("metadata source = %q, want gross currency mode", meta.Source)
	}
}

func TestNodePricingCPURAMSplitReconcilesTotalCost(t *testing.T) {
	tests := []struct {
		name       string
		serverType string
		vcpu       int64
		ramGiB     float64
		hourly     float64
	}{
		{name: "balanced", serverType: "cx22", vcpu: 2, ramGiB: 4, hourly: 0.036},
		{name: "memory heavy", serverType: "ccx33", vcpu: 8, ramGiB: 32, hourly: 0.276},
		{name: "fractional RAM", serverType: "cpx11", vcpu: 2, ramGiB: 2.5, hourly: 0.007},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := testHetznerProviderWithPricingData("net", []HetznerServer{{
				Project: "prod", ID: 100, Type: tc.serverType, Location: "fsn1", VCPU: tc.vcpu, RAMGiB: tc.ramGiB,
			}}, map[locationTypeKey]HetznerHourlyPrice{
				{Location: "fsn1", Type: tc.serverType}: {NetHourly: tc.hourly},
			})

			node, _, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://100"})
			if err != nil {
				t.Fatalf("NodePricing() error = %v", err)
			}

			cost := mustParseNodeFloat(t, "Cost", node.Cost)
			vcpuCost := mustParseNodeFloat(t, "VCPUCost", node.VCPUCost)
			ramCost := mustParseNodeFloat(t, "RAMCost", node.RAMCost)
			got := vcpuCost*float64(tc.vcpu) + ramCost*tc.ramGiB
			assertFloatNear(t, got, cost, nodePricingTolerance)
			assertFloatNear(t, vcpuCost, tc.hourly*0.5/float64(tc.vcpu), nodePricingTolerance)
			assertFloatNear(t, ramCost, tc.hourly*0.5/tc.ramGiB, nodePricingTolerance)
		})
	}
}

func TestNodePricingFailsForInvalidCPURAMSplitInputs(t *testing.T) {
	tests := []struct {
		name   string
		vcpu   int64
		ramGiB float64
	}{
		{name: "zero vcpu", vcpu: 0, ramGiB: 4},
		{name: "negative vcpu", vcpu: -1, ramGiB: 4},
		{name: "zero ram", vcpu: 2, ramGiB: 0},
		{name: "negative ram", vcpu: 2, ramGiB: -1},
		{name: "nan ram", vcpu: 2, ramGiB: math.NaN()},
		{name: "infinite ram", vcpu: 2, ramGiB: math.Inf(1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := testHetznerProviderWithPricingData("net", []HetznerServer{{
				Project: "prod", ID: 100, Type: "cx22", Location: "fsn1", VCPU: tc.vcpu, RAMGiB: tc.ramGiB,
			}}, map[locationTypeKey]HetznerHourlyPrice{
				{Location: "fsn1", Type: "cx22"}: {NetHourly: 0.036},
			})

			node, _, err := provider.NodePricing(&hetznerKey{ProviderID: "hcloud://100"})
			if err == nil {
				t.Fatal("NodePricing() error = nil, want invalid CPU/RAM error")
			}
			if !strings.Contains(err.Error(), "invalid server resources") {
				t.Fatalf("NodePricing() error = %q, want invalid resources message", err)
			}
			if node != nil {
				t.Fatalf("NodePricing() node = %#v, want nil on invalid resources", node)
			}
		})
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

func testHetznerProviderWithPricingData(currencyMode string, servers []HetznerServer, prices map[locationTypeKey]HetznerHourlyPrice) *Hetzner {
	data := newHetznerPricingData()
	data.CurrencyMode = currencyMode
	for key, price := range prices {
		data.ServerPrices[key] = price
	}
	for _, server := range servers {
		data.Servers[projectResourceKey{Project: server.Project, ID: server.ID}] = server
		if !containsString(data.Projects, server.Project) {
			data.Projects = append(data.Projects, server.Project)
		}
	}
	return &Hetzner{
		Config:      testProviderConfig{},
		pricingData: data,
	}
}

func assertNodeField(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func mustParseNodeFloat(t *testing.T, name, value string) float64 {
	t.Helper()
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("parse %s %q: %v", name, value, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		t.Fatalf("%s = %f, want finite value", name, parsed)
	}
	return parsed
}

func assertFloatNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("got %.12f, want %.12f within %.12f", got, want, tolerance)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
