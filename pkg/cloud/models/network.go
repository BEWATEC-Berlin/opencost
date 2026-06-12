package models

// TODO: used for dynamic cloud provider price fetching.
// determine what identifies a load balancer in the json returned from the cloud provider pricing API call
// type LBKey interface {
// }

// Network is the struct by which the provider and cost model communicate
// network prices. Each cost field is expressed as provider currency per GiB of
// network usage. The cost model multiplies these values by NetworkGiBResult
// usage vectors, so providers with decimal GB/TB pricing APIs must convert to
// GiB before returning values here.
type Network struct {
	ZoneNetworkEgressCost     float64
	RegionNetworkEgressCost   float64
	InternetNetworkEgressCost float64
	NatGatewayEgressCost      float64
	NatGatewayIngressCost     float64
}

// LoadBalancer is the interface by which the provider and cost model communicate LoadBalancer prices.
// The provider will best-effort try to fill out this struct.
type LoadBalancer struct {
	IngressIPAddresses []string `json:"IngressIPAddresses"`
	Cost               float64  `json:"hourlyCost"`
}
