package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeProjectClient struct {
	pricing       hcloud.Pricing
	servers       []*hcloud.Server
	volumes       []*hcloud.Volume
	loadBalancers []*hcloud.LoadBalancer
	primaryIPs    []*hcloud.PrimaryIP
	floatingIPs   []*hcloud.FloatingIP
	err           error

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeProjectClient) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

func (f *fakeProjectClient) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, count := range f.calls {
		total += count
	}
	return total
}

func (f *fakeProjectClient) Pricing(context.Context) (hcloud.Pricing, error) {
	f.count("pricing")
	if f.err != nil {
		return hcloud.Pricing{}, f.err
	}
	return f.pricing, nil
}

func (f *fakeProjectClient) Servers(context.Context, string) ([]*hcloud.Server, error) {
	f.count("servers")
	if f.err != nil {
		return nil, f.err
	}
	return f.servers, nil
}

func (f *fakeProjectClient) Volumes(context.Context, string) ([]*hcloud.Volume, error) {
	f.count("volumes")
	if f.err != nil {
		return nil, f.err
	}
	return f.volumes, nil
}

func (f *fakeProjectClient) LoadBalancers(context.Context, string) ([]*hcloud.LoadBalancer, error) {
	f.count("load_balancers")
	if f.err != nil {
		return nil, f.err
	}
	return f.loadBalancers, nil
}

func (f *fakeProjectClient) PrimaryIPs(context.Context, string) ([]*hcloud.PrimaryIP, error) {
	f.count("primary_ips")
	if f.err != nil {
		return nil, f.err
	}
	return f.primaryIPs, nil
}

func (f *fakeProjectClient) FloatingIPs(context.Context, string) ([]*hcloud.FloatingIP, error) {
	f.count("floating_ips")
	if f.err != nil {
		return nil, f.err
	}
	return f.floatingIPs, nil
}

func TestDownloadPricingDataPopulatesNormalizedLookups(t *testing.T) {
	client := &fakeProjectClient{
		pricing: testHetznerPricing(),
		servers: []*hcloud.Server{{
			ID:              100,
			Name:            "worker-1",
			Location:        &hcloud.Location{Name: "fsn1"},
			IncludedTraffic: 1_000_000_000_000,
			OutgoingTraffic: 2_000_000_000_000,
			ServerType: &hcloud.ServerType{
				Name:   "cpx21",
				Cores:  3,
				Memory: 4,
			},
		}},
		volumes: []*hcloud.Volume{{
			ID:       10,
			Name:     "data",
			Location: &hcloud.Location{Name: "fsn1"},
			Size:     100,
		}},
		loadBalancers: []*hcloud.LoadBalancer{{
			ID:               20,
			Name:             "ingress",
			Location:         &hcloud.Location{Name: "fsn1"},
			LoadBalancerType: &hcloud.LoadBalancerType{Name: "lb11"},
			IncludedTraffic:  1_000_000_000_000,
			OutgoingTraffic:  2_000_000_000_000,
		}},
		primaryIPs: []*hcloud.PrimaryIP{{
			ID:       30,
			Name:     "primary",
			Type:     hcloud.PrimaryIPTypeIPv4,
			Location: &hcloud.Location{Name: "fsn1"},
		}},
		floatingIPs: []*hcloud.FloatingIP{{
			ID:           40,
			Name:         "floating",
			Type:         hcloud.FloatingIPTypeIPv6,
			HomeLocation: &hcloud.Location{Name: "fsn1"},
		}},
	}
	provider := newFakeProvider(client, []HetznerProject{{Name: "prod", Token: "prod-token"}})

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}

	data, err := provider.snapshotPricingData()
	if err != nil {
		t.Fatalf("snapshotPricingData() error = %v", err)
	}

	if got := data.ServerPrices[locationTypeKey{Location: "fsn1", Type: "cpx21"}].NetHourly; got != 0.012 {
		t.Fatalf("server hourly net = %.4f, want 0.0120", got)
	}
	if got := data.VolumePrices["default"].NetPerGBHour; got != 0.0476/hetznerHoursPerMonth {
		t.Fatalf("volume per GB hour net = %.12f, want %.12f", got, 0.0476/hetznerHoursPerMonth)
	}
	if got := data.LoadBalancerPrices[locationTypeKey{Location: "fsn1", Type: "lb11"}].NetHourly; got != 0.0081 {
		t.Fatalf("load balancer hourly net = %.4f, want 0.0081", got)
	}
	if got := data.PrimaryIPPrices[locationTypeKey{Location: "fsn1", Type: "ipv4"}].NetHourly; got != 0.0015 {
		t.Fatalf("primary IP hourly net = %.4f, want 0.0015", got)
	}
	if got := data.FloatingIPPrices[locationTypeKey{Location: "fsn1", Type: "ipv6"}].NetHourly; got != 0.365/hetznerHoursPerMonth {
		t.Fatalf("floating IP hourly net = %.12f, want %.12f", got, 0.365/hetznerHoursPerMonth)
	}
	if got := data.ServerTrafficPrices[locationTypeKey{Location: "fsn1", Type: "cpx21"}].NetPerTB; got != 1 {
		t.Fatalf("server traffic net = %.4f, want 1", got)
	}
	if got := data.LoadBalancerTrafficPrices[locationTypeKey{Location: "fsn1", Type: "lb11"}].NetPerTB; got != 2 {
		t.Fatalf("load balancer traffic net = %.4f, want 2", got)
	}
	if len(data.Servers) != 1 || len(data.Volumes) != 1 || len(data.LoadBalancers) != 1 || len(data.PrimaryIPs) != 1 || len(data.FloatingIPs) != 1 || len(data.Traffic) != 2 {
		t.Fatalf("unexpected resource counts: %#v", data.Summary())
	}
}

func TestDownloadPricingDataPrefixesResourceKeysByProject(t *testing.T) {
	clients := map[string]*fakeProjectClient{
		"prod-token":  {pricing: testHetznerPricing(), servers: []*hcloud.Server{testServer(100, "prod-worker")}},
		"stage-token": {pricing: testHetznerPricing(), servers: []*hcloud.Server{testServer(100, "stage-worker")}},
	}
	provider := &Hetzner{
		ConfigData: HetznerConfig{Projects: []HetznerProject{
			{Name: "prod", Token: "prod-token"},
			{Name: "stage", Token: "stage-token"},
		}},
		NewClient: func(token string) hetznerProjectClient {
			return clients[token]
		},
	}

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}

	data, err := provider.snapshotPricingData()
	if err != nil {
		t.Fatalf("snapshotPricingData() error = %v", err)
	}

	if len(data.Servers) != 2 {
		t.Fatalf("len(Servers) = %d, want 2", len(data.Servers))
	}
	if data.Servers[projectResourceKey{Project: "prod", ID: 100}].Name != "prod-worker" {
		t.Fatalf("prod server missing or collided: %#v", data.Servers)
	}
	if data.Servers[projectResourceKey{Project: "stage", ID: 100}].Name != "stage-worker" {
		t.Fatalf("stage server missing or collided: %#v", data.Servers)
	}
}

func TestDownloadPricingDataReturnsProjectErrorsWithoutToken(t *testing.T) {
	const credential = "hcloud-fixture-credential"
	provider := newFakeProvider(&fakeProjectClient{
		err: errors.New("request failed with Authorization: Bearer " + credential),
	}, []HetznerProject{{Name: "prod", Token: credential}})

	err := provider.DownloadPricingData()
	if err == nil {
		t.Fatal("DownloadPricingData() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `project "prod"`) {
		t.Fatalf("DownloadPricingData() error = %q, want project name", err)
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatalf("DownloadPricingData() leaked token in error: %q", err)
	}
}

func TestPricingReadsDoNotRefetchAndRefreshDoes(t *testing.T) {
	client := &fakeProjectClient{pricing: testHetznerPricing(), servers: []*hcloud.Server{testServer(100, "worker")}}
	provider := newFakeProvider(client, []HetznerProject{{Name: "prod", Token: "prod-token"}})

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}
	callsAfterRefresh := client.totalCalls()

	for i := 0; i < 5; i++ {
		if _, err := provider.AllNodePricing(); err != nil {
			t.Fatalf("AllNodePricing() error = %v", err)
		}
		_ = provider.PricingSourceSummary()
		_ = provider.PricingSourceStatus()
	}
	if got := client.totalCalls(); got != callsAfterRefresh {
		t.Fatalf("read calls refetched data: got %d calls, want %d", got, callsAfterRefresh)
	}

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("second DownloadPricingData() error = %v", err)
	}
	if got := client.totalCalls(); got <= callsAfterRefresh {
		t.Fatalf("refresh did not refetch data: got %d calls, previous %d", got, callsAfterRefresh)
	}
}

func TestPricingSourceSummaryAndStatusDoNotLeakSecrets(t *testing.T) {
	const credential = "hcloud-fixture-redaction-value"
	provider := newFakeProvider(&fakeProjectClient{pricing: testHetznerPricing(), servers: []*hcloud.Server{testServer(100, "worker")}}, []HetznerProject{{Name: "prod", Token: credential}})

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}

	summary := provider.PricingSourceSummary()
	status := provider.PricingSourceStatus()
	combined := strings.Join([]string{toJSONForTest(t, summary), toJSONForTest(t, status)}, "\n")
	if strings.Contains(combined, credential) {
		t.Fatalf("pricing source output leaked token: %s", combined)
	}
	if !strings.Contains(combined, "prod") {
		t.Fatalf("pricing source output should include project name, got: %s", combined)
	}
}

func TestPVPricingRespectsGrossCurrencyMode(t *testing.T) {
	provider := newFakeProvider(&fakeProjectClient{pricing: testHetznerPricing()}, []HetznerProject{{Name: "prod", Token: "prod-token"}})
	provider.ConfigData.CurrencyMode = "gross"

	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}

	pv, err := provider.PVPricing(&hetznerPVKey{
		StorageClassName: "hcloud-volumes",
		ProviderID:       "hcloud://10",
		Region:           "fsn1",
	})
	if err != nil {
		t.Fatalf("PVPricing() error = %v", err)
	}
	if got, want := pv.Cost, "0.00007753424657534247"; got != want {
		t.Fatalf("PV cost = %q, want gross per GB hour %q", got, want)
	}
}

func TestCacheReadsAreConcurrencySafe(t *testing.T) {
	provider := newFakeProvider(&fakeProjectClient{pricing: testHetznerPricing(), servers: []*hcloud.Server{testServer(100, "worker")}}, []HetznerProject{{Name: "prod", Token: "prod-token"}})
	if err := provider.DownloadPricingData(); err != nil {
		t.Fatalf("DownloadPricingData() error = %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 20*50)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := provider.AllNodePricing(); err != nil {
					errCh <- err
					return
				}
				_ = provider.PricingSourceSummary()
				_ = provider.PricingSourceStatus()
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent read returned error: %v", err)
	}
}

func newFakeProvider(client *fakeProjectClient, projects []HetznerProject) *Hetzner {
	return &Hetzner{
		ConfigData: HetznerConfig{Projects: projects},
		NewClient: func(string) hetznerProjectClient {
			return client
		},
	}
}

func testServer(id int64, name string) *hcloud.Server {
	return &hcloud.Server{
		ID:       id,
		Name:     name,
		Location: &hcloud.Location{Name: "fsn1"},
		ServerType: &hcloud.ServerType{
			Name:   "cpx21",
			Cores:  3,
			Memory: 4,
		},
	}
}

func testHetznerPricing() hcloud.Pricing {
	return hcloud.Pricing{
		Volume: hcloud.VolumePricing{
			PerGBMonthly: hcloud.Price{Net: "0.0476", Gross: "0.0566"},
		},
		LoadBalancerTypes: []hcloud.LoadBalancerTypePricing{{
			LoadBalancerType: &hcloud.LoadBalancerType{Name: "lb11"},
			Pricings: []hcloud.LoadBalancerTypeLocationPricing{{
				Location:     &hcloud.Location{Name: "fsn1"},
				Hourly:       hcloud.Price{Net: "0.0081", Gross: "0.0096"},
				PerTBTraffic: hcloud.Price{Net: "2", Gross: "2.38"},
			}},
		}},
		PrimaryIPs: []hcloud.PrimaryIPPricing{{
			Type: "ipv4",
			Pricings: []hcloud.PrimaryIPTypePricing{{
				Location: "fsn1",
				Hourly:   hcloud.PrimaryIPPrice{Net: "0.0015", Gross: "0.0018"},
			}},
		}},
		FloatingIPs: []hcloud.FloatingIPTypePricing{{
			Type: hcloud.FloatingIPTypeIPv6,
			Pricings: []hcloud.FloatingIPTypeLocationPricing{{
				Location: &hcloud.Location{Name: "fsn1"},
				Monthly:  hcloud.Price{Net: "0.365", Gross: "0.4344"},
			}},
		}},
		ServerTypes: []hcloud.ServerTypePricing{{
			ServerType: &hcloud.ServerType{Name: "cpx21"},
			Pricings: []hcloud.ServerTypeLocationPricing{{
				Location:     &hcloud.Location{Name: "fsn1"},
				Hourly:       hcloud.Price{Net: "0.0120", Gross: "0.0143"},
				PerTBTraffic: hcloud.Price{Net: "1", Gross: "1.19"},
			}},
		}},
	}
}

func toJSONForTest(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(data)
}
