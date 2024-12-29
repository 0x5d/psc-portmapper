package gcp

import (
	"strconv"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func FirewallNeedsUpdate(fw *computepb.FirewallPolicy, expectedPorts []int32) bool {
	if fw == nil || len(fw.Rules) != 1 {
		return true
	}
	rule := fw.Rules[0]
	if rule == nil || rule.Match == nil || len(rule.Match.Layer4Configs) != 1 {
		return true
	}
	l4cfg := rule.Match.Layer4Configs[0]
	if l4cfg.IpProtocol == nil || *l4cfg.IpProtocol != "tcp" {
		return true
	}
	strPorts := toStr(expectedPorts)
	portSet := map[string]struct{}{}
	for _, p := range strPorts {
		portSet[p] = struct{}{}
	}
	for _, p := range l4cfg.Ports {
		if _, ok := portSet[p]; !ok {
			return true
		}
	}
	return false
}

func toStr(is []int32) []string {
	ss := make([]string, 0, len(is))
	for _, i := range is {
		ss = append(ss, strconv.Itoa(int(i)))
	}
	return ss
}
