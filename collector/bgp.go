package collector

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	bgpSubsystem = "bgp"

	bgpLabels     = []string{"vrf", "address_family"}
	bgpPeerLabels = append(bgpLabels, "peer")

	bgpDesc = map[string]*prometheus.Desc{
		"bgpRibEntries":       colPromDesc(bgpSubsystem, "rib_entries", "Number of routes in the RIB.", bgpLabels),
		"bgpRibMemUsgage":     colPromDesc(bgpSubsystem, "rib_memory_usage_bytes", "Memory consumbed by the RIB.", bgpLabels),
		"bgpPeerTotal":        colPromDesc(bgpSubsystem, "peers", "Number peers configured.", bgpLabels),
		"bgpPeerMemUsage":     colPromDesc(bgpSubsystem, "peers_memory_usage_bytes", "Memory consumed by peers.", bgpLabels),
		"bgpPeerGrps":         colPromDesc(bgpSubsystem, "peer_groups", "Number of peer groups configured.", bgpLabels),
		"bgpPeerGrpsMemUsage": colPromDesc(bgpSubsystem, "peer_groups_memory_bytes", "Memory consumed by peer groups.", bgpLabels),

		"bgpPeerMsgIn":     colPromDesc(bgpSubsystem, "message_input_total", "Number of received messages.", bgpPeerLabels),
		"bgpPeerMsgOut":    colPromDesc(bgpSubsystem, "message_output_total", "Number of sent messages.", bgpPeerLabels),
		"bgpPeerPrfAct":    colPromDesc(bgpSubsystem, "prefixes_active", "Number of active prefixes.", bgpPeerLabels),
		"bgpPeerUp":        colPromDesc(bgpSubsystem, "peer_up", "State of the peer (1 = Established, 0 = Down).", bgpPeerLabels),
		"bgpPeerUptimeSec": colPromDesc(bgpSubsystem, "peer_uptime_seconds", "How long has the peer been up.", bgpPeerLabels),
	}

	bgpErrors      = []error{}
	totalBGPErrors = 0.0
)

// BGPCollector collects BGP metrics, implemented as per prometheus.Collector interface.
type BGPCollector struct{}

// NewBGPCollector returns a BGPCollector struct.
func NewBGPCollector() *BGPCollector {
	return &BGPCollector{}
}

// Name of the collector. Used to populate flag name.
func (*BGPCollector) Name() string {
	return bgpSubsystem
}

// Help describes the metrics this collector scrapes. Used to populate flag help.
func (*BGPCollector) Help() string {
	return "Collect BGP Metrics."
}

// EnabledByDefault describes whether this collector is enabled by default. Used to populate flag default.
func (*BGPCollector) EnabledByDefault() bool {
	return true
}

// Describe implemented as per the prometheus.Collector interface.
func (*BGPCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range bgpDesc {
		ch <- desc
	}
}

// Collect implemented as per the prometheus.Collector interface.
func (c *BGPCollector) Collect(ch chan<- prometheus.Metric) {

	addressFamilies := []string{"ipv4", "ipv6"}
	addressFamilyModifiers := []string{"unicast"}

	for _, af := range addressFamilies {
		for _, afMod := range addressFamilyModifiers {
			jsonBGPSum, err := getBGPSummary(af, afMod)
			if err != nil {
				totalBGPErrors++
				bgpErrors = append(bgpErrors, fmt.Errorf("cannot get bgp %s %s summary: %s", af, afMod, err))
			} else {
				if err := processBGPSummary(ch, jsonBGPSum, af+afMod); err != nil {
					totalBGPErrors++
					bgpErrors = append(bgpErrors, fmt.Errorf("%s", err))
				}
			}
		}
	}
}

// CollectErrors returns what errors have been gathered.
func (*BGPCollector) CollectErrors() []error {
	return bgpErrors
}

// CollectTotalErrors returns total errors.
func (*BGPCollector) CollectTotalErrors() float64 {
	return totalBGPErrors
}

func getBGPSummary(addressFamily string, addressFamilyModifier string) ([]byte, error) {
	args := []string{"-c", fmt.Sprintf("show ip bgp vrf all %s %s summary json", addressFamily, addressFamilyModifier)}
	output, err := exec.Command(vtyshPath, args...).Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func processBGPSummary(ch chan<- prometheus.Metric, jsonBGPSum []byte, addressFamily string) error {
	var jsonMap map[string]bgpProcess

	if err := json.Unmarshal(jsonBGPSum, &jsonMap); err != nil {
		return fmt.Errorf("cannot unmarshal bgp summary json: %s", err)
	}

	for vrfName, vrfData := range jsonMap {
		// The labels are "vrf", "address_family",
		bgpProcLabels := []string{strings.ToLower(vrfName), strings.ToLower(addressFamily)}
		// No point collecting metrics if no peers configured.
		if vrfData.PeerCount != 0 {

			newGauge(ch, bgpDesc["bgpRibEntries"], vrfData.RIBCount, bgpProcLabels...)
			newGauge(ch, bgpDesc["bgpRibMemUsgage"], vrfData.RIBMemory, bgpProcLabels...)
			newGauge(ch, bgpDesc["bgpPeerTotal"], vrfData.PeerCount, bgpProcLabels...)
			newGauge(ch, bgpDesc["bgpPeerMemUsage"], vrfData.PeerMemory, bgpProcLabels...)
			newGauge(ch, bgpDesc["bgpPeerGrps"], vrfData.PeerGroupCount, bgpProcLabels...)
			newGauge(ch, bgpDesc["bgpPeerGrpsMemUsage"], vrfData.PeerGroupMemory, bgpProcLabels...)

			for peerIP, peerData := range vrfData.Peers {
				// The labels are "vrf", "address_family", "peer"
				bgpPeerLabels := []string{strings.ToLower(vrfName), strings.ToLower(addressFamily), peerIP}

				newCounter(ch, bgpDesc["bgpPeerMsgIn"], peerData.MsgRcvd, bgpPeerLabels...)
				newCounter(ch, bgpDesc["bgpPeerMsgOut"], peerData.MsgSent, bgpPeerLabels...)
				newGauge(ch, bgpDesc["bgpPeerPrfAct"], peerData.PrefixReceivedCount, bgpPeerLabels...)
				newGauge(ch, bgpDesc["bgpPeerUptimeSec"], peerData.PeerUptimeMsec*0.001, bgpPeerLabels...)

				peerState := 0.0
				if strings.ToLower(peerData.State) == "established" {
					peerState = 1
				}

				newGauge(ch, bgpDesc["bgpPeerUp"], peerState, bgpPeerLabels...)
			}
		}
	}
	return nil
}

type bgpProcess struct {
	RouterID        string
	AS              int
	RIBCount        float64
	RIBMemory       float64
	PeerCount       float64
	PeerMemory      float64
	PeerGroupCount  float64
	PeerGroupMemory float64
	Peers           map[string]*bgpPeerSession
}

type bgpPeerSession struct {
	State               string
	MsgRcvd             float64
	MsgSent             float64
	PeerUptimeMsec      float64
	PrefixReceivedCount float64
}
