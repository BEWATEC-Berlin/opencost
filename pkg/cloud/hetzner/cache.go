package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/opencost/opencost/pkg/env"
)

const hetznerHoursPerMonth = 730.0

type HetznerConfig struct {
	CurrencyMode string           `json:"currency_mode"`
	Projects     []HetznerProject `json:"projects"`
	Include      HetznerInclude   `json:"include"`
}

type HetznerProject struct {
	Name          string `json:"name"`
	Token         string `json:"token"`
	LabelSelector string `json:"label_selector"`
}

type HetznerInclude struct {
	Servers       bool `json:"servers"`
	Volumes       bool `json:"volumes"`
	LoadBalancers bool `json:"load_balancers"`
	PrimaryIPs    bool `json:"primary_ips"`
	FloatingIPs   bool `json:"floating_ips"`
	Traffic       bool `json:"traffic"`
}

type HetznerPricingData struct {
	LoadedAt                  time.Time
	CurrencyMode              string
	Projects                  []string
	ServerPrices              map[locationTypeKey]HetznerHourlyPrice
	VolumePrices              map[string]HetznerVolumePrice
	LoadBalancerPrices        map[locationTypeKey]HetznerHourlyPrice
	PrimaryIPPrices           map[locationTypeKey]HetznerHourlyPrice
	FloatingIPPrices          map[locationTypeKey]HetznerHourlyPrice
	ServerTrafficPrices       map[locationTypeKey]HetznerTrafficPrice
	LoadBalancerTrafficPrices map[locationTypeKey]HetznerTrafficPrice
	Servers                   map[projectResourceKey]HetznerServer
	Volumes                   map[projectResourceKey]HetznerVolume
	LoadBalancers             map[projectResourceKey]HetznerLoadBalancer
	PrimaryIPs                map[projectResourceKey]HetznerIP
	FloatingIPs               map[projectResourceKey]HetznerIP
	Traffic                   map[projectResourceKey]HetznerTraffic
}

type HetznerHourlyPrice struct {
	NetHourly   float64 `json:"netHourly"`
	GrossHourly float64 `json:"grossHourly"`
}

type HetznerVolumePrice struct {
	NetPerGBHour   float64 `json:"netPerGBHour"`
	GrossPerGBHour float64 `json:"grossPerGBHour"`
}

type HetznerTrafficPrice struct {
	NetPerTB   float64 `json:"netPerTB"`
	GrossPerTB float64 `json:"grossPerTB"`
}

type HetznerServer struct {
	Project      string  `json:"project"`
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Location     string  `json:"location"`
	VCPU         int64   `json:"vcpu"`
	RAMGiB       float64 `json:"ramGiB"`
	NetHourly    float64 `json:"netHourly"`
	GrossHourly  float64 `json:"grossHourly"`
	ProviderID   string  `json:"providerID"`
	OutgoingTB   float64 `json:"outgoingTB"`
	IncludedTB   float64 `json:"includedTB"`
	NetTrafficTB float64 `json:"netTrafficTB"`
}

type HetznerVolume struct {
	Project     string  `json:"project"`
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Location    string  `json:"location"`
	SizeGB      int     `json:"sizeGB"`
	NetHourly   float64 `json:"netHourly"`
	GrossHourly float64 `json:"grossHourly"`
}

type HetznerLoadBalancer struct {
	Project      string  `json:"project"`
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Location     string  `json:"location"`
	NetHourly    float64 `json:"netHourly"`
	GrossHourly  float64 `json:"grossHourly"`
	OutgoingTB   float64 `json:"outgoingTB"`
	IncludedTB   float64 `json:"includedTB"`
	NetTrafficTB float64 `json:"netTrafficTB"`
}

type HetznerIP struct {
	Project     string  `json:"project"`
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Location    string  `json:"location"`
	NetHourly   float64 `json:"netHourly"`
	GrossHourly float64 `json:"grossHourly"`
}

type HetznerTraffic struct {
	Project              string  `json:"project"`
	ResourceType         string  `json:"resourceType"`
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Location             string  `json:"location"`
	OutgoingBytes        uint64  `json:"outgoingBytes"`
	IncludedTrafficBytes uint64  `json:"includedTrafficBytes"`
	NetPerTB             float64 `json:"netPerTB"`
	GrossPerTB           float64 `json:"grossPerTB"`
}

type HetznerPricingSummary struct {
	Loaded                        bool      `json:"loaded"`
	LoadedAt                      time.Time `json:"loadedAt,omitempty"`
	Projects                      []string  `json:"projects"`
	ServerPriceCount              int       `json:"serverPriceCount"`
	VolumePriceCount              int       `json:"volumePriceCount"`
	LoadBalancerPriceCount        int       `json:"loadBalancerPriceCount"`
	PrimaryIPPriceCount           int       `json:"primaryIPPriceCount"`
	FloatingIPPriceCount          int       `json:"floatingIPPriceCount"`
	ServerTrafficPriceCount       int       `json:"serverTrafficPriceCount"`
	LoadBalancerTrafficPriceCount int       `json:"loadBalancerTrafficPriceCount"`
	ServerCount                   int       `json:"serverCount"`
	VolumeCount                   int       `json:"volumeCount"`
	LoadBalancerCount             int       `json:"loadBalancerCount"`
	PrimaryIPCount                int       `json:"primaryIPCount"`
	FloatingIPCount               int       `json:"floatingIPCount"`
	TrafficCount                  int       `json:"trafficCount"`
	Error                         string    `json:"error,omitempty"`
}

type locationTypeKey struct {
	Location string
	Type     string
}

type projectResourceKey struct {
	Project string
	ID      int64
}

type hetznerProjectClient interface {
	Pricing(ctx context.Context) (hcloud.Pricing, error)
	Servers(ctx context.Context, labelSelector string) ([]*hcloud.Server, error)
	Volumes(ctx context.Context, labelSelector string) ([]*hcloud.Volume, error)
	LoadBalancers(ctx context.Context, labelSelector string) ([]*hcloud.LoadBalancer, error)
	PrimaryIPs(ctx context.Context, labelSelector string) ([]*hcloud.PrimaryIP, error)
	FloatingIPs(ctx context.Context, labelSelector string) ([]*hcloud.FloatingIP, error)
}

type liveProjectClient struct {
	client *hcloud.Client
}

func (h *Hetzner) DownloadPricingData() error {
	cfg, err := h.loadHetznerConfig()
	if err != nil {
		h.setPricingError(err.Error())
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	include := effectiveInclude(cfg.Include)
	next := newHetznerPricingData()
	next.LoadedAt = time.Now().UTC()
	next.CurrencyMode = cfg.CurrencyMode

	for _, project := range cfg.Projects {
		if err := h.fetchProject(ctx, next, project, include); err != nil {
			safeErr := h.sanitizeProjectError(project, err)
			h.setPricingError(safeErr.Error())
			return safeErr
		}
		next.Projects = append(next.Projects, project.Name)
	}

	h.DownloadPricingDataLock.Lock()
	defer h.DownloadPricingDataLock.Unlock()
	h.pricingData = next
	h.lastPricingError = ""
	return nil
}

func (h *Hetzner) AllNodePricing() (interface{}, error) {
	data, err := h.snapshotPricingData()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return map[locationTypeKey]HetznerHourlyPrice{}, nil
	}
	return data.ServerPrices, nil
}

func (h *Hetzner) PricingSourceStatus() map[string]*models.PricingSource {
	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()

	return map[string]*models.PricingSource{
		HetznerCloudPricingSource: {
			Name:      HetznerCloudPricingSource,
			Enabled:   true,
			Available: h.pricingData != nil,
			Error:     h.lastPricingError,
		},
	}
}

func (h *Hetzner) PricingSourceSummary() interface{} {
	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()

	if h.pricingData == nil {
		return HetznerPricingSummary{
			Loaded: false,
			Error:  h.lastPricingError,
		}
	}
	summary := h.pricingData.Summary()
	summary.Error = h.lastPricingError
	return summary
}

func (h *Hetzner) snapshotPricingData() (*HetznerPricingData, error) {
	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()
	if h.pricingData == nil {
		return nil, nil
	}
	return h.pricingData.Clone(), nil
}

func (h *Hetzner) fetchProject(ctx context.Context, data *HetznerPricingData, project HetznerProject, include HetznerInclude) error {
	client := h.newClient(project.Token)
	pricing, err := client.Pricing(ctx)
	if err != nil {
		return fmt.Errorf("get pricing: %w", err)
	}

	if err := normalizePricing(data, pricing); err != nil {
		return err
	}

	if include.Servers {
		servers, err := client.Servers(ctx, project.LabelSelector)
		if err != nil {
			return fmt.Errorf("list servers: %w", err)
		}
		for _, server := range servers {
			if server == nil || server.ServerType == nil || server.Location == nil {
				continue
			}
			key := locationTypeKey{Location: server.Location.Name, Type: server.ServerType.Name}
			price, ok := data.ServerPrices[key]
			if !ok {
				return fmt.Errorf("server %q: no hourly price for type %q in location %q", server.Name, server.ServerType.Name, server.Location.Name)
			}
			resourceKey := projectResourceKey{Project: project.Name, ID: server.ID}
			data.Servers[resourceKey] = HetznerServer{
				Project:      project.Name,
				ID:           server.ID,
				Name:         server.Name,
				Type:         server.ServerType.Name,
				Location:     server.Location.Name,
				VCPU:         int64(server.ServerType.Cores),
				RAMGiB:       float64(server.ServerType.Memory),
				NetHourly:    price.NetHourly,
				GrossHourly:  price.GrossHourly,
				ProviderID:   fmt.Sprintf("hcloud://%d", server.ID),
				OutgoingTB:   bytesToTB(server.OutgoingTraffic),
				IncludedTB:   bytesToTB(server.IncludedTraffic),
				NetTrafficTB: billableTrafficTB(server.OutgoingTraffic, server.IncludedTraffic),
			}
			if include.Traffic {
				trafficPrice := data.ServerTrafficPrices[key]
				data.Traffic[projectResourceKey{Project: project.Name, ID: trafficID("server", server.ID)}] = HetznerTraffic{
					Project:              project.Name,
					ResourceType:         "server",
					ID:                   server.ID,
					Name:                 server.Name,
					Location:             server.Location.Name,
					OutgoingBytes:        server.OutgoingTraffic,
					IncludedTrafficBytes: server.IncludedTraffic,
					NetPerTB:             trafficPrice.NetPerTB,
					GrossPerTB:           trafficPrice.GrossPerTB,
				}
			}
		}
	}

	if include.Volumes {
		volumes, err := client.Volumes(ctx, project.LabelSelector)
		if err != nil {
			return fmt.Errorf("list volumes: %w", err)
		}
		volumePrice := data.VolumePrices["default"]
		for _, volume := range volumes {
			if volume == nil || volume.Location == nil {
				continue
			}
			data.Volumes[projectResourceKey{Project: project.Name, ID: volume.ID}] = HetznerVolume{
				Project:     project.Name,
				ID:          volume.ID,
				Name:        volume.Name,
				Location:    volume.Location.Name,
				SizeGB:      volume.Size,
				NetHourly:   volumePrice.NetPerGBHour * float64(volume.Size),
				GrossHourly: volumePrice.GrossPerGBHour * float64(volume.Size),
			}
		}
	}

	if include.LoadBalancers {
		loadBalancers, err := client.LoadBalancers(ctx, project.LabelSelector)
		if err != nil {
			return fmt.Errorf("list load balancers: %w", err)
		}
		for _, lb := range loadBalancers {
			if lb == nil || lb.Location == nil || lb.LoadBalancerType == nil {
				continue
			}
			key := locationTypeKey{Location: lb.Location.Name, Type: lb.LoadBalancerType.Name}
			price, ok := data.LoadBalancerPrices[key]
			if !ok {
				return fmt.Errorf("load balancer %q: no hourly price for type %q in location %q", lb.Name, lb.LoadBalancerType.Name, lb.Location.Name)
			}
			data.LoadBalancers[projectResourceKey{Project: project.Name, ID: lb.ID}] = HetznerLoadBalancer{
				Project:      project.Name,
				ID:           lb.ID,
				Name:         lb.Name,
				Type:         lb.LoadBalancerType.Name,
				Location:     lb.Location.Name,
				NetHourly:    price.NetHourly,
				GrossHourly:  price.GrossHourly,
				OutgoingTB:   bytesToTB(lb.OutgoingTraffic),
				IncludedTB:   bytesToTB(lb.IncludedTraffic),
				NetTrafficTB: billableTrafficTB(lb.OutgoingTraffic, lb.IncludedTraffic),
			}
			if include.Traffic {
				trafficPrice := data.LoadBalancerTrafficPrices[key]
				data.Traffic[projectResourceKey{Project: project.Name, ID: trafficID("load_balancer", lb.ID)}] = HetznerTraffic{
					Project:              project.Name,
					ResourceType:         "load_balancer",
					ID:                   lb.ID,
					Name:                 lb.Name,
					Location:             lb.Location.Name,
					OutgoingBytes:        lb.OutgoingTraffic,
					IncludedTrafficBytes: lb.IncludedTraffic,
					NetPerTB:             trafficPrice.NetPerTB,
					GrossPerTB:           trafficPrice.GrossPerTB,
				}
			}
		}
	}

	if include.PrimaryIPs {
		primaryIPs, err := client.PrimaryIPs(ctx, project.LabelSelector)
		if err != nil {
			return fmt.Errorf("list primary IPs: %w", err)
		}
		for _, ip := range primaryIPs {
			if ip == nil || ip.Location == nil {
				continue
			}
			key := locationTypeKey{Location: ip.Location.Name, Type: string(ip.Type)}
			price, ok := data.PrimaryIPPrices[key]
			if !ok {
				return fmt.Errorf("primary IP %q: no hourly price for type %q in location %q", ip.Name, ip.Type, ip.Location.Name)
			}
			data.PrimaryIPs[projectResourceKey{Project: project.Name, ID: ip.ID}] = HetznerIP{
				Project:     project.Name,
				ID:          ip.ID,
				Name:        ip.Name,
				Type:        string(ip.Type),
				Location:    ip.Location.Name,
				NetHourly:   price.NetHourly,
				GrossHourly: price.GrossHourly,
			}
		}
	}

	if include.FloatingIPs {
		floatingIPs, err := client.FloatingIPs(ctx, project.LabelSelector)
		if err != nil {
			return fmt.Errorf("list floating IPs: %w", err)
		}
		for _, ip := range floatingIPs {
			if ip == nil || ip.HomeLocation == nil {
				continue
			}
			key := locationTypeKey{Location: ip.HomeLocation.Name, Type: string(ip.Type)}
			price, ok := data.FloatingIPPrices[key]
			if !ok {
				return fmt.Errorf("floating IP %q: no hourly price for type %q in location %q", ip.Name, ip.Type, ip.HomeLocation.Name)
			}
			data.FloatingIPs[projectResourceKey{Project: project.Name, ID: ip.ID}] = HetznerIP{
				Project:     project.Name,
				ID:          ip.ID,
				Name:        ip.Name,
				Type:        string(ip.Type),
				Location:    ip.HomeLocation.Name,
				NetHourly:   price.NetHourly,
				GrossHourly: price.GrossHourly,
			}
		}
	}

	return nil
}

func normalizePricing(data *HetznerPricingData, pricing hcloud.Pricing) error {
	netVolume, err := parsePrice(pricing.Volume.PerGBMonthly.Net)
	if err != nil {
		return fmt.Errorf("parse volume net monthly price: %w", err)
	}
	grossVolume, err := parsePrice(pricing.Volume.PerGBMonthly.Gross)
	if err != nil {
		return fmt.Errorf("parse volume gross monthly price: %w", err)
	}
	data.VolumePrices["default"] = HetznerVolumePrice{
		NetPerGBHour:   netVolume / hetznerHoursPerMonth,
		GrossPerGBHour: grossVolume / hetznerHoursPerMonth,
	}

	for _, serverType := range pricing.ServerTypes {
		if serverType.ServerType == nil {
			continue
		}
		for _, locationPricing := range serverType.Pricings {
			if locationPricing.Location == nil {
				continue
			}
			key := locationTypeKey{Location: locationPricing.Location.Name, Type: serverType.ServerType.Name}
			hourly, err := parseHourlyPrice(locationPricing.Hourly)
			if err != nil {
				return fmt.Errorf("server type %q location %q: %w", serverType.ServerType.Name, locationPricing.Location.Name, err)
			}
			data.ServerPrices[key] = hourly
			data.ServerTrafficPrices[key] = parseTrafficPrice(locationPricing.PerTBTraffic)
		}
	}

	for _, lbType := range pricing.LoadBalancerTypes {
		if lbType.LoadBalancerType == nil {
			continue
		}
		for _, locationPricing := range lbType.Pricings {
			if locationPricing.Location == nil {
				continue
			}
			key := locationTypeKey{Location: locationPricing.Location.Name, Type: lbType.LoadBalancerType.Name}
			hourly, err := parseHourlyPrice(locationPricing.Hourly)
			if err != nil {
				return fmt.Errorf("load balancer type %q location %q: %w", lbType.LoadBalancerType.Name, locationPricing.Location.Name, err)
			}
			data.LoadBalancerPrices[key] = hourly
			data.LoadBalancerTrafficPrices[key] = parseTrafficPrice(locationPricing.PerTBTraffic)
		}
	}

	for _, primaryIP := range pricing.PrimaryIPs {
		for _, locationPricing := range primaryIP.Pricings {
			key := locationTypeKey{Location: locationPricing.Location, Type: primaryIP.Type}
			net, err := parsePrice(locationPricing.Hourly.Net)
			if err != nil {
				return fmt.Errorf("primary IP type %q location %q net hourly: %w", primaryIP.Type, locationPricing.Location, err)
			}
			gross, err := parsePrice(locationPricing.Hourly.Gross)
			if err != nil {
				return fmt.Errorf("primary IP type %q location %q gross hourly: %w", primaryIP.Type, locationPricing.Location, err)
			}
			data.PrimaryIPPrices[key] = HetznerHourlyPrice{NetHourly: net, GrossHourly: gross}
		}
	}

	for _, floatingIP := range pricing.FloatingIPs {
		for _, locationPricing := range floatingIP.Pricings {
			if locationPricing.Location == nil {
				continue
			}
			net, err := parsePrice(locationPricing.Monthly.Net)
			if err != nil {
				return fmt.Errorf("floating IP type %q location %q net monthly: %w", floatingIP.Type, locationPricing.Location.Name, err)
			}
			gross, err := parsePrice(locationPricing.Monthly.Gross)
			if err != nil {
				return fmt.Errorf("floating IP type %q location %q gross monthly: %w", floatingIP.Type, locationPricing.Location.Name, err)
			}
			data.FloatingIPPrices[locationTypeKey{Location: locationPricing.Location.Name, Type: string(floatingIP.Type)}] = HetznerHourlyPrice{
				NetHourly:   net / hetznerHoursPerMonth,
				GrossHourly: gross / hetznerHoursPerMonth,
			}
		}
	}

	return nil
}

func parseHourlyPrice(price hcloud.Price) (HetznerHourlyPrice, error) {
	net, err := parsePrice(price.Net)
	if err != nil {
		return HetznerHourlyPrice{}, fmt.Errorf("parse net hourly price: %w", err)
	}
	gross, err := parsePrice(price.Gross)
	if err != nil {
		return HetznerHourlyPrice{}, fmt.Errorf("parse gross hourly price: %w", err)
	}
	return HetznerHourlyPrice{NetHourly: net, GrossHourly: gross}, nil
}

func parseTrafficPrice(price hcloud.Price) HetznerTrafficPrice {
	return HetznerTrafficPrice{
		NetPerTB:   parsePriceOrZero(price.Net),
		GrossPerTB: parsePriceOrZero(price.Gross),
	}
}

func parsePriceOrZero(raw string) float64 {
	parsed, err := parsePrice(raw)
	if err != nil {
		return 0
	}
	return parsed
}

func parsePrice(raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", raw, err)
	}
	return value, nil
}

func (h *Hetzner) loadHetznerConfig() (HetznerConfig, error) {
	if len(h.ConfigData.Projects) > 0 {
		return validateHetznerConfig(h.ConfigData)
	}

	path := strings.TrimSpace(h.ConfigPath)
	if path == "" {
		path = env.GetHetznerConfigPath()
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return HetznerConfig{}, fmt.Errorf("read Hetzner config: %w", err)
		}
		var cfg HetznerConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return HetznerConfig{}, fmt.Errorf("decode Hetzner config: %w", err)
		}
		return validateHetznerConfig(cfg)
	}

	token := strings.TrimSpace(env.GetHetznerAPIToken())
	if token == "" {
		token = strings.TrimSpace(env.GetCloudProviderAPIKey())
	}
	if token == "" {
		return HetznerConfig{}, fmt.Errorf("Hetzner: no project config found; set %s or %s", env.HetznerConfigPathEnvVar, env.HetznerAPITokenEnvVar)
	}

	name := strings.TrimSpace(h.ClusterAccountID)
	if name == "" {
		name = "default"
	}
	return validateHetznerConfig(HetznerConfig{
		Projects: []HetznerProject{{
			Name:  name,
			Token: token,
		}},
	})
}

func validateHetznerConfig(cfg HetznerConfig) (HetznerConfig, error) {
	if cfg.CurrencyMode == "" {
		cfg.CurrencyMode = "net"
	}
	if cfg.CurrencyMode != "net" && cfg.CurrencyMode != "gross" {
		return HetznerConfig{}, fmt.Errorf("currency_mode must be net or gross")
	}
	if len(cfg.Projects) == 0 {
		return HetznerConfig{}, fmt.Errorf("at least one Hetzner project is required")
	}

	seen := map[string]struct{}{}
	for idx := range cfg.Projects {
		cfg.Projects[idx].Name = strings.TrimSpace(cfg.Projects[idx].Name)
		cfg.Projects[idx].Token = strings.TrimSpace(cfg.Projects[idx].Token)
		if cfg.Projects[idx].Name == "" {
			return HetznerConfig{}, fmt.Errorf("projects[%d].name is required", idx)
		}
		if cfg.Projects[idx].Token == "" {
			return HetznerConfig{}, fmt.Errorf("projects[%d].token is required", idx)
		}
		if _, ok := seen[cfg.Projects[idx].Name]; ok {
			return HetznerConfig{}, fmt.Errorf("duplicate project name %q", cfg.Projects[idx].Name)
		}
		seen[cfg.Projects[idx].Name] = struct{}{}
	}

	return cfg, nil
}

func effectiveInclude(include HetznerInclude) HetznerInclude {
	if include.Servers || include.Volumes || include.LoadBalancers || include.PrimaryIPs || include.FloatingIPs || include.Traffic {
		return include
	}
	return HetznerInclude{
		Servers:       true,
		Volumes:       true,
		LoadBalancers: true,
		PrimaryIPs:    true,
		FloatingIPs:   true,
		Traffic:       true,
	}
}

func (h *Hetzner) newClient(token string) hetznerProjectClient {
	if h.NewClient != nil {
		return h.NewClient(token)
	}
	return liveProjectClient{
		client: hcloud.NewClient(
			hcloud.WithToken(token),
			hcloud.WithApplication("opencost", "dev"),
		),
	}
}

func (c liveProjectClient) Pricing(ctx context.Context) (hcloud.Pricing, error) {
	pricing, _, err := c.client.Pricing.Get(ctx)
	return pricing, err
}

func (c liveProjectClient) Servers(ctx context.Context, labelSelector string) ([]*hcloud.Server, error) {
	return c.client.Server.AllWithOpts(ctx, hcloud.ServerListOpts{ListOpts: hcloud.ListOpts{LabelSelector: labelSelector}})
}

func (c liveProjectClient) Volumes(ctx context.Context, labelSelector string) ([]*hcloud.Volume, error) {
	return c.client.Volume.AllWithOpts(ctx, hcloud.VolumeListOpts{ListOpts: hcloud.ListOpts{LabelSelector: labelSelector}})
}

func (c liveProjectClient) LoadBalancers(ctx context.Context, labelSelector string) ([]*hcloud.LoadBalancer, error) {
	return c.client.LoadBalancer.AllWithOpts(ctx, hcloud.LoadBalancerListOpts{ListOpts: hcloud.ListOpts{LabelSelector: labelSelector}})
}

func (c liveProjectClient) PrimaryIPs(ctx context.Context, labelSelector string) ([]*hcloud.PrimaryIP, error) {
	return c.client.PrimaryIP.AllWithOpts(ctx, hcloud.PrimaryIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: labelSelector}})
}

func (c liveProjectClient) FloatingIPs(ctx context.Context, labelSelector string) ([]*hcloud.FloatingIP, error) {
	return c.client.FloatingIP.AllWithOpts(ctx, hcloud.FloatingIPListOpts{ListOpts: hcloud.ListOpts{LabelSelector: labelSelector}})
}

func (h *Hetzner) sanitizeProjectError(project HetznerProject, err error) error {
	message := err.Error()
	for _, token := range h.knownTokens(project) {
		message = strings.ReplaceAll(message, token, "[redacted]")
	}
	return fmt.Errorf("fetch Hetzner project %q: %s", project.Name, message)
}

func (h *Hetzner) knownTokens(project HetznerProject) []string {
	tokens := []string{project.Token}
	for _, configuredProject := range h.ConfigData.Projects {
		tokens = append(tokens, configuredProject.Token)
	}
	tokens = append(tokens, env.GetHetznerAPIToken(), env.GetCloudProviderAPIKey())

	filtered := make([]string, 0, len(tokens))
	seen := map[string]struct{}{}
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		filtered = append(filtered, token)
	}
	return filtered
}

func (h *Hetzner) setPricingError(message string) {
	h.DownloadPricingDataLock.Lock()
	defer h.DownloadPricingDataLock.Unlock()
	h.lastPricingError = message
}

func newHetznerPricingData() *HetznerPricingData {
	return &HetznerPricingData{
		ServerPrices:              map[locationTypeKey]HetznerHourlyPrice{},
		VolumePrices:              map[string]HetznerVolumePrice{},
		LoadBalancerPrices:        map[locationTypeKey]HetznerHourlyPrice{},
		PrimaryIPPrices:           map[locationTypeKey]HetznerHourlyPrice{},
		FloatingIPPrices:          map[locationTypeKey]HetznerHourlyPrice{},
		ServerTrafficPrices:       map[locationTypeKey]HetznerTrafficPrice{},
		LoadBalancerTrafficPrices: map[locationTypeKey]HetznerTrafficPrice{},
		Servers:                   map[projectResourceKey]HetznerServer{},
		Volumes:                   map[projectResourceKey]HetznerVolume{},
		LoadBalancers:             map[projectResourceKey]HetznerLoadBalancer{},
		PrimaryIPs:                map[projectResourceKey]HetznerIP{},
		FloatingIPs:               map[projectResourceKey]HetznerIP{},
		Traffic:                   map[projectResourceKey]HetznerTraffic{},
	}
}

func (d *HetznerPricingData) Clone() *HetznerPricingData {
	if d == nil {
		return nil
	}
	clone := newHetznerPricingData()
	clone.LoadedAt = d.LoadedAt
	clone.CurrencyMode = d.CurrencyMode
	clone.Projects = append([]string(nil), d.Projects...)
	for key, value := range d.ServerPrices {
		clone.ServerPrices[key] = value
	}
	for key, value := range d.VolumePrices {
		clone.VolumePrices[key] = value
	}
	for key, value := range d.LoadBalancerPrices {
		clone.LoadBalancerPrices[key] = value
	}
	for key, value := range d.PrimaryIPPrices {
		clone.PrimaryIPPrices[key] = value
	}
	for key, value := range d.FloatingIPPrices {
		clone.FloatingIPPrices[key] = value
	}
	for key, value := range d.ServerTrafficPrices {
		clone.ServerTrafficPrices[key] = value
	}
	for key, value := range d.LoadBalancerTrafficPrices {
		clone.LoadBalancerTrafficPrices[key] = value
	}
	for key, value := range d.Servers {
		clone.Servers[key] = value
	}
	for key, value := range d.Volumes {
		clone.Volumes[key] = value
	}
	for key, value := range d.LoadBalancers {
		clone.LoadBalancers[key] = value
	}
	for key, value := range d.PrimaryIPs {
		clone.PrimaryIPs[key] = value
	}
	for key, value := range d.FloatingIPs {
		clone.FloatingIPs[key] = value
	}
	for key, value := range d.Traffic {
		clone.Traffic[key] = value
	}
	return clone
}

func (d *HetznerPricingData) Summary() HetznerPricingSummary {
	if d == nil {
		return HetznerPricingSummary{}
	}
	return HetznerPricingSummary{
		Loaded:                        true,
		LoadedAt:                      d.LoadedAt,
		Projects:                      append([]string(nil), d.Projects...),
		ServerPriceCount:              len(d.ServerPrices),
		VolumePriceCount:              len(d.VolumePrices),
		LoadBalancerPriceCount:        len(d.LoadBalancerPrices),
		PrimaryIPPriceCount:           len(d.PrimaryIPPrices),
		FloatingIPPriceCount:          len(d.FloatingIPPrices),
		ServerTrafficPriceCount:       len(d.ServerTrafficPrices),
		LoadBalancerTrafficPriceCount: len(d.LoadBalancerTrafficPrices),
		ServerCount:                   len(d.Servers),
		VolumeCount:                   len(d.Volumes),
		LoadBalancerCount:             len(d.LoadBalancers),
		PrimaryIPCount:                len(d.PrimaryIPs),
		FloatingIPCount:               len(d.FloatingIPs),
		TrafficCount:                  len(d.Traffic),
	}
}

func bytesToTB(bytes uint64) float64 {
	return float64(bytes) / 1_000_000_000_000
}

func billableTrafficTB(outgoing, included uint64) float64 {
	if outgoing <= included {
		return 0
	}
	return bytesToTB(outgoing - included)
}

func trafficID(resourceType string, id int64) int64 {
	if resourceType == "load_balancer" {
		return -id
	}
	return id
}
