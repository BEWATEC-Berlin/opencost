package hetzner

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/opencost/opencost/core/pkg/clustercache"
	coreenv "github.com/opencost/opencost/core/pkg/env"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util"
	"github.com/opencost/opencost/core/pkg/util/json"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/opencost/opencost/pkg/cloud/utils"
	"github.com/opencost/opencost/pkg/env"
	v1 "k8s.io/api/core/v1"
)

const HetznerCloudPricingSource = "Hetzner Cloud Pricing"

const (
	hcloudProviderIDPrefix = "hcloud://"
	hcloudCSIDriver        = "csi.hetzner.cloud"
	hcloudStorageClass     = "hcloud-volumes"
	hetznerNodeUsageType   = "hetzner-cloud"
	gibibyte               = 1024 * 1024 * 1024
)

type Hetzner struct {
	Clientset               clustercache.ClusterCache
	Config                  models.ProviderConfig
	ClusterRegion           string
	ClusterAccountID        string
	DownloadPricingDataLock sync.RWMutex

	ConfigData HetznerConfig
	ConfigPath string
	NewClient  func(token string) hetznerProjectClient

	pricingData      *HetznerPricingData
	lastPricingError string
}

type hetznerKey struct {
	Labels     map[string]string
	ProviderID string
}

func (k *hetznerKey) ID() string {
	return k.ProviderID
}

func (k *hetznerKey) Features() string {
	region, _ := util.GetRegion(k.Labels)
	instanceType, _ := util.GetInstanceType(k.Labels)
	return region + "," + instanceType
}

func (k *hetznerKey) GPUType() string {
	return ""
}

func (k *hetznerKey) GPUCount() int {
	return 0
}

type hetznerPVKey struct {
	StorageClassName       string
	StorageClassParameters map[string]string
	ProviderID             string
	Region                 string
	SizeGB                 int64
	IsHCloudCSI            bool
}

func (k *hetznerPVKey) ID() string {
	return k.ProviderID
}

func (k *hetznerPVKey) Features() string {
	return k.Region
}

func (k *hetznerPVKey) GetStorageClass() string {
	return k.StorageClassName
}

func (h *Hetzner) ClusterInfo() (map[string]string, error) {
	remoteEnabled := env.IsRemoteEnabled()

	m := map[string]string{
		"name":              "Hetzner Cluster #1",
		"provider":          opencost.HetznerProvider,
		"region":            h.ClusterRegion,
		"account":           h.ClusterAccountID,
		"remoteReadEnabled": strconv.FormatBool(remoteEnabled),
		"id":                coreenv.GetClusterID(),
	}

	c, err := h.GetConfig()
	if err != nil {
		return nil, err
	}
	if c.ClusterName != "" {
		m["name"] = c.ClusterName
	}

	return m, nil
}

func (*Hetzner) GetAddresses() ([]byte, error) {
	return nil, nil
}

func (*Hetzner) GetDisks() ([]byte, error) {
	return nil, nil
}

func (*Hetzner) GetOrphanedResources() ([]models.OrphanedResource, error) {
	return nil, errors.New("not implemented")
}

func (h *Hetzner) NodePricing(key models.Key) (*models.Node, models.PricingMetadata, error) {
	meta := hetznerPricingMetadata("net")
	if key == nil {
		return nil, meta, fmt.Errorf("Hetzner node pricing: nil node key")
	}

	serverID, err := parseHCloudServerID(key.ID())
	if err != nil {
		return nil, meta, err
	}

	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()
	if h.pricingData == nil {
		return nil, meta, fmt.Errorf("Hetzner node pricing: pricing cache is empty")
	}
	meta = hetznerPricingMetadata(h.pricingData.CurrencyMode)

	server, err := h.resolveCachedServer(h.pricingData, serverID)
	if err != nil {
		return nil, meta, err
	}
	if strings.TrimSpace(server.Type) == "" {
		return nil, meta, fmt.Errorf("Hetzner node pricing: server ID %d missing server type", serverID)
	}
	if strings.TrimSpace(server.Location) == "" {
		return nil, meta, fmt.Errorf("Hetzner node pricing: server ID %d missing server location", serverID)
	}

	priceKey := locationTypeKey{Location: server.Location, Type: server.Type}
	price, ok := h.pricingData.ServerPrices[priceKey]
	if !ok {
		return nil, meta, fmt.Errorf("Hetzner node pricing: server ID %d missing server price for type %q in location %q", serverID, server.Type, server.Location)
	}

	hourlyCost := price.NetHourly
	if h.pricingData.CurrencyMode == "gross" {
		hourlyCost = price.GrossHourly
	}

	vcpuCost, ramCost, err := splitHetznerNodeCost(hourlyCost, server.VCPU, server.RAMGiB)
	if err != nil {
		return nil, meta, fmt.Errorf("Hetzner node pricing: server ID %d invalid server resources: %w", serverID, err)
	}

	return &models.Node{
		Cost:         formatHetznerFloat(hourlyCost),
		VCPU:         strconv.FormatInt(server.VCPU, 10),
		RAM:          formatHetznerFloat(server.RAMGiB),
		RAMBytes:     strconv.FormatInt(int64(math.Round(server.RAMGiB*gibibyte)), 10),
		VCPUCost:     formatHetznerFloat(vcpuCost),
		RAMCost:      formatHetznerFloat(ramCost),
		InstanceType: server.Type,
		Region:       server.Location,
		ProviderID:   key.ID(),
		UsageType:    hetznerNodeUsageType,
		PricingType:  models.Api,
		ArchType:     hetznerKeyArch(key),
	}, meta, nil
}

func parseHCloudServerID(providerID string) (int64, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0, fmt.Errorf("Hetzner node pricing: empty provider ID")
	}
	if !strings.HasPrefix(providerID, hcloudProviderIDPrefix) {
		return 0, fmt.Errorf("Hetzner node pricing: expected hcloud:// provider ID, got %q", providerID)
	}

	rawID := strings.TrimPrefix(providerID, hcloudProviderIDPrefix)
	if rawID == "" {
		return 0, fmt.Errorf("Hetzner node pricing: empty server ID in provider ID %q", providerID)
	}
	serverID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("Hetzner node pricing: parse server ID %q: %w", rawID, err)
	}
	if serverID <= 0 {
		return 0, fmt.Errorf("Hetzner node pricing: server ID %d must be positive", serverID)
	}
	return serverID, nil
}

func (h *Hetzner) resolveCachedServer(data *HetznerPricingData, serverID int64) (HetznerServer, error) {
	project := strings.TrimSpace(h.ClusterAccountID)
	var matches []HetznerServer
	for key, server := range data.Servers {
		if key.ID == serverID {
			matches = append(matches, server)
		}
	}
	switch len(matches) {
	case 0:
		return HetznerServer{}, fmt.Errorf("Hetzner node pricing: server ID %d not found in cached Hetzner servers", serverID)
	case 1:
		return matches[0], nil
	default:
		if project != "" {
			server, ok := data.Servers[projectResourceKey{Project: project, ID: serverID}]
			if ok {
				return server, nil
			}
			return HetznerServer{}, fmt.Errorf("Hetzner node pricing: server ID %d matched multiple Hetzner projects, but no match was found for configured project %q", serverID, project)
		}
		return HetznerServer{}, fmt.Errorf("Hetzner node pricing: server ID %d matched multiple Hetzner projects; set ClusterAccountID to the Hetzner project name", serverID)
	}
}

func splitHetznerNodeCost(hourlyCost float64, vcpu int64, ramGiB float64) (float64, float64, error) {
	if math.IsNaN(hourlyCost) || math.IsInf(hourlyCost, 0) {
		return 0, 0, fmt.Errorf("invalid hourly cost %f", hourlyCost)
	}
	if vcpu <= 0 {
		return 0, 0, fmt.Errorf("invalid vCPU %d", vcpu)
	}
	if ramGiB <= 0 || math.IsNaN(ramGiB) || math.IsInf(ramGiB, 0) {
		return 0, 0, fmt.Errorf("invalid RAM GiB %f", ramGiB)
	}

	// Hetzner exposes a single server hourly price. To keep allocation semantics
	// stable and auditable, split that price into equal CPU and RAM cost pools,
	// then divide each pool by the server's resource quantity. This guarantees:
	// VCPUCost*VCPU + RAMCost*RAMGiB == Cost, subject to float precision.
	return (hourlyCost * 0.5) / float64(vcpu), (hourlyCost * 0.5) / ramGiB, nil
}

func hetznerPricingMetadata(rawCurrencyMode string) models.PricingMetadata {
	currencyMode := "net"
	if strings.TrimSpace(rawCurrencyMode) != "" {
		currencyMode = strings.TrimSpace(rawCurrencyMode)
	}
	return models.PricingMetadata{
		Currency: "EUR",
		Source:   fmt.Sprintf("%s (%s)", HetznerCloudPricingSource, currencyMode),
	}
}

func hetznerKeyArch(key models.Key) string {
	hk, ok := key.(*hetznerKey)
	if !ok || hk.Labels == nil {
		return ""
	}
	if arch := hk.Labels["kubernetes.io/arch"]; arch != "" {
		return arch
	}
	return hk.Labels["beta.kubernetes.io/arch"]
}

func formatHetznerFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (*Hetzner) GpuPricing(map[string]string) (string, error) {
	return "", nil
}

func (h *Hetzner) PVPricing(pvk models.PVKey) (*models.PV, error) {
	if pvk == nil {
		return &models.PV{}, nil
	}

	storageClass := pvk.GetStorageClass()
	providerID := strings.TrimSpace(pvk.ID())
	region := pvk.Features()
	sizeGB := int64(0)
	var parameters map[string]string
	isHCloudCSI := storageClass == hcloudStorageClass || strings.HasPrefix(providerID, hcloudProviderIDPrefix)
	if key, ok := pvk.(*hetznerPVKey); ok {
		sizeGB = key.SizeGB
		parameters = copyStringMap(key.StorageClassParameters)
		isHCloudCSI = isHCloudCSI || key.IsHCloudCSI
	}
	if !isHCloudCSI {
		return &models.PV{}, nil
	}

	volumeID, hasVolumeID, err := parseHCloudVolumeID(providerID)
	if err != nil {
		return nil, err
	}
	if !hasVolumeID {
		log.Debugf("Hetzner PV pricing unavailable: missing CSI volume handle, storageClass=%q region=%q", storageClass, region)
		return &models.PV{}, nil
	}
	providerID = fmt.Sprintf("%s%d", hcloudProviderIDPrefix, volumeID)

	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()

	if h.pricingData == nil {
		log.Debugf("Hetzner PV pricing unavailable: pricing cache is empty, storageClass=%q region=%q", storageClass, region)
		return &models.PV{}, nil
	}
	volumePrice, ok := h.pricingData.VolumePrices["default"]
	if !ok {
		log.Debugf("Hetzner PV pricing unavailable: default volume price missing, storageClass=%q region=%q", storageClass, region)
		return &models.PV{}, nil
	}
	if len(h.pricingData.Volumes) > 0 {
		volume, err := h.resolveCachedVolume(h.pricingData, volumeID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(volume.Location) != "" {
			region = volume.Location
		}
		if volume.SizeGB > 0 {
			sizeGB = int64(volume.SizeGB)
		}
	}

	cost := volumePrice.NetPerGBHour
	if h.pricingData.CurrencyMode == "gross" {
		cost = volumePrice.GrossPerGBHour
	}

	return &models.PV{
		Cost:       formatHetznerFloat(cost),
		CostPerIO:  "0",
		Class:      storageClass,
		Size:       formatHetznerSizeGB(sizeGB),
		Region:     region,
		ProviderID: providerID,
		Parameters: parameters,
	}, nil
}

func parseHCloudVolumeID(providerID string) (int64, bool, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0, false, nil
	}

	rawID := providerID
	if strings.HasPrefix(providerID, hcloudProviderIDPrefix) {
		rawID = strings.TrimPrefix(providerID, hcloudProviderIDPrefix)
	} else if !isNumericString(providerID) {
		return 0, true, fmt.Errorf("Hetzner PV pricing: expected hcloud volume ID as numeric CSI handle or hcloud:// provider ID, got %q", providerID)
	}
	if rawID == "" {
		return 0, true, fmt.Errorf("Hetzner PV pricing: empty volume ID in provider ID %q", providerID)
	}

	volumeID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return 0, true, fmt.Errorf("Hetzner PV pricing: parse volume ID %q: %w", rawID, err)
	}
	if volumeID <= 0 {
		return 0, true, fmt.Errorf("Hetzner PV pricing: volume ID %d must be positive", volumeID)
	}
	return volumeID, true, nil
}

func (h *Hetzner) resolveCachedVolume(data *HetznerPricingData, volumeID int64) (HetznerVolume, error) {
	project := strings.TrimSpace(h.ClusterAccountID)
	var matches []HetznerVolume
	for key, volume := range data.Volumes {
		if key.ID == volumeID {
			matches = append(matches, volume)
		}
	}
	switch len(matches) {
	case 0:
		return HetznerVolume{}, fmt.Errorf("Hetzner PV pricing: volume ID %d not found in cached Hetzner volumes", volumeID)
	case 1:
		return matches[0], nil
	default:
		if project != "" {
			volume, ok := data.Volumes[projectResourceKey{Project: project, ID: volumeID}]
			if ok {
				return volume, nil
			}
			return HetznerVolume{}, fmt.Errorf("Hetzner PV pricing: volume ID %d matched multiple Hetzner projects, but no match was found for configured project %q", volumeID, project)
		}
		return HetznerVolume{}, fmt.Errorf("Hetzner PV pricing: volume ID %d matched multiple Hetzner projects; set ClusterAccountID to the Hetzner project name", volumeID)
	}
}

func (*Hetzner) NetworkPricing() (*models.Network, error) {
	return &models.Network{}, nil
}

// LoadBalancerPricing has no OpenCost service-specific key to inspect, so the
// Hetzner provider cannot map a Kubernetes Service to an exact Hetzner LB type
// or resource. Use the cheapest cached LB type hourly price as a deterministic
// Kubernetes Service allocation fallback, and deliberately ignore cached LB
// resource instances to avoid double-counting non-Kubernetes load balancers.
func (h *Hetzner) LoadBalancerPricing() (*models.LoadBalancer, error) {
	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()

	if h.pricingData == nil || len(h.pricingData.LoadBalancerPrices) == 0 {
		return &models.LoadBalancer{}, nil
	}

	cost, ok := cheapestHetznerLoadBalancerCost(h.pricingData.LoadBalancerPrices, h.pricingData.CurrencyMode)
	if !ok {
		return &models.LoadBalancer{}, nil
	}

	return &models.LoadBalancer{Cost: cost}, nil
}

func (*Hetzner) GetKey(labels map[string]string, node *clustercache.Node) models.Key {
	providerID := ""
	if node != nil {
		providerID = node.SpecProviderID
	}
	return &hetznerKey{
		Labels:     labels,
		ProviderID: providerID,
	}
}

func (*Hetzner) GetPVKey(pv *clustercache.PersistentVolume, parameters map[string]string, defaultRegion string) models.PVKey {
	if pv == nil {
		return &hetznerPVKey{
			StorageClassParameters: copyStringMap(parameters),
			Region:                 defaultRegion,
		}
	}

	providerID := ""
	isHCloudCSI := false
	if pv.Spec.CSI != nil {
		isHCloudCSI = strings.EqualFold(strings.TrimSpace(pv.Spec.CSI.Driver), hcloudCSIDriver)
		providerID = normalizeHCloudVolumeProviderID(pv.Spec.CSI.VolumeHandle)
	}

	return &hetznerPVKey{
		StorageClassName:       pv.Spec.StorageClassName,
		StorageClassParameters: copyStringMap(parameters),
		ProviderID:             providerID,
		Region:                 hetznerPVRegion(pv, parameters, defaultRegion),
		SizeGB:                 hetznerPVSizeGB(pv),
		IsHCloudCSI:            isHCloudCSI,
	}
}

func normalizeHCloudVolumeProviderID(volumeHandle string) string {
	volumeHandle = strings.TrimSpace(volumeHandle)
	if volumeHandle == "" {
		return ""
	}
	if strings.HasPrefix(volumeHandle, hcloudProviderIDPrefix) {
		return volumeHandle
	}
	if isNumericString(volumeHandle) {
		return hcloudProviderIDPrefix + volumeHandle
	}
	return volumeHandle
}

func isNumericString(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func hetznerPVRegion(pv *clustercache.PersistentVolume, parameters map[string]string, defaultRegion string) string {
	region := strings.TrimSpace(defaultRegion)
	for _, key := range []string{"location", "topology.kubernetes.io/zone", "topology.kubernetes.io/region"} {
		if value := strings.TrimSpace(parameters[key]); value != "" {
			region = value
			break
		}
	}
	if pv == nil || pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return region
	}
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Operator != v1.NodeSelectorOpIn || len(expr.Values) == 0 {
				continue
			}
			if expr.Key == v1.LabelTopologyZone || expr.Key == v1.LabelTopologyRegion || expr.Key == "failure-domain.beta.kubernetes.io/zone" || expr.Key == "failure-domain.beta.kubernetes.io/region" {
				if value := strings.TrimSpace(expr.Values[0]); value != "" {
					return value
				}
			}
		}
	}
	return region
}

func hetznerPVSizeGB(pv *clustercache.PersistentVolume) int64 {
	if pv == nil {
		return 0
	}
	storage := pv.Spec.Capacity.Storage()
	if storage == nil {
		return 0
	}
	return storage.Value() / gibibyte
}

func formatHetznerSizeGB(sizeGB int64) string {
	if sizeGB <= 0 {
		return ""
	}
	return strconv.FormatInt(sizeGB, 10)
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func cheapestHetznerLoadBalancerCost(prices map[locationTypeKey]HetznerHourlyPrice, currencyMode string) (float64, bool) {
	var bestKey locationTypeKey
	var bestCost float64
	found := false
	for key, price := range prices {
		cost := price.NetHourly
		if currencyMode == "gross" {
			cost = price.GrossHourly
		}
		if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
			continue
		}
		if !found || cost < bestCost || (cost == bestCost && compareLocationTypeKey(key, bestKey) < 0) {
			bestCost = cost
			bestKey = key
			found = true
		}
	}
	return bestCost, found
}

func compareLocationTypeKey(a, b locationTypeKey) int {
	if a.Location < b.Location {
		return -1
	}
	if a.Location > b.Location {
		return 1
	}
	if a.Type < b.Type {
		return -1
	}
	if a.Type > b.Type {
		return 1
	}
	return 0
}

func (h *Hetzner) UpdateConfig(r io.Reader, updateType string) (*models.CustomPricing, error) {
	cp, err := h.Config.Update(func(c *models.CustomPricing) error {
		a := make(map[string]interface{})
		if err := json.NewDecoder(r).Decode(&a); err != nil {
			return err
		}
		for k, v := range a {
			kUpper := utils.ToTitle.String(k)
			vstr, ok := v.(string)
			if !ok {
				return fmt.Errorf("type error while updating config for %s", kUpper)
			}
			if err := models.SetCustomPricingField(c, kUpper, vstr); err != nil {
				return fmt.Errorf("error setting custom pricing field: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return cp, err
	}

	return cp, h.DownloadPricingData()
}

func (h *Hetzner) UpdateConfigFromConfigMap(a map[string]string) (*models.CustomPricing, error) {
	return h.Config.UpdateFromMap(a)
}

func (h *Hetzner) GetConfig() (*models.CustomPricing, error) {
	c, err := h.Config.GetCustomPricingData()
	if err != nil {
		return nil, err
	}
	if c.Discount == "" {
		c.Discount = "0%"
	}
	if c.NegotiatedDiscount == "" {
		c.NegotiatedDiscount = "0%"
	}
	if c.CurrencyCode == "" {
		c.CurrencyCode = "EUR"
	}
	return c, nil
}

func (*Hetzner) GetManagementPlatform() (string, error) {
	return "", nil
}

func (*Hetzner) ApplyReservedInstancePricing(map[string]*models.Node) {}

func (*Hetzner) ServiceAccountStatus() *models.ServiceAccountStatus {
	return &models.ServiceAccountStatus{
		Checks: []*models.ServiceAccountCheck{},
	}
}

func (*Hetzner) ClusterManagementPricing() (string, float64, error) {
	return "", 0.0, nil
}

func (*Hetzner) CombinedDiscountForNode(instanceType string, isPreemptible bool, defaultDiscount, negotiatedDiscount float64) float64 {
	return 1.0 - ((1.0 - defaultDiscount) * (1.0 - negotiatedDiscount))
}

func (h *Hetzner) Regions() []string {
	regionOverrides := env.GetRegionOverrideList()
	if len(regionOverrides) > 0 {
		return regionOverrides
	}
	if h.ClusterRegion != "" {
		return []string{h.ClusterRegion}
	}
	return []string{"fsn1", "nbg1", "hel1"}
}
