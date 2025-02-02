package gcp

import (
	"sort"
	"strconv"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func FirewallNeedsUpdate(fw *computepb.FirewallPolicy, expectedPorts map[int32]struct{}) bool {
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

func toStr(is map[int32]struct{}) []string {
	ss := make([]string, 0, len(is))
	for p, _ := range is {
		ss = append(ss, strconv.Itoa(int(p)))
	}
	sort.Slice(ss, func(i int, j int) bool { return ss[i] < ss[j] })
	return ss
}
