package hetzner

import (
	"errors"
	"fmt"
	"io"
	"strconv"
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
)

const HetznerCloudPricingSource = "Hetzner Cloud Pricing"

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

func (*Hetzner) NodePricing(models.Key) (*models.Node, models.PricingMetadata, error) {
	return nil, models.PricingMetadata{}, errors.New("not implemented")
}

func (*Hetzner) GpuPricing(map[string]string) (string, error) {
	return "", nil
}

func (h *Hetzner) PVPricing(pvk models.PVKey) (*models.PV, error) {
	h.DownloadPricingDataLock.RLock()
	defer h.DownloadPricingDataLock.RUnlock()

	if h.pricingData == nil {
		log.Debugf("Hetzner PV pricing unavailable: pricing cache is empty, storageClass=%q region=%q", pvk.GetStorageClass(), pvk.Features())
		return &models.PV{}, nil
	}
	volumePrice, ok := h.pricingData.VolumePrices["default"]
	if !ok {
		log.Debugf("Hetzner PV pricing unavailable: default volume price missing, storageClass=%q region=%q", pvk.GetStorageClass(), pvk.Features())
		return &models.PV{}, nil
	}

	return &models.PV{
		Cost:   strconv.FormatFloat(volumePrice.NetPerGBHour, 'f', -1, 64),
		Class:  pvk.GetStorageClass(),
		Region: pvk.Features(),
	}, nil
}

func (*Hetzner) NetworkPricing() (*models.Network, error) {
	return &models.Network{}, nil
}

func (*Hetzner) LoadBalancerPricing() (*models.LoadBalancer, error) {
	return &models.LoadBalancer{}, nil
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
	providerID := ""
	if pv.Spec.CSI != nil {
		providerID = pv.Spec.CSI.VolumeHandle
	}

	return &hetznerPVKey{
		StorageClassName:       pv.Spec.StorageClassName,
		StorageClassParameters: parameters,
		ProviderID:             providerID,
		Region:                 defaultRegion,
	}
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
