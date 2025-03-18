package gcp

import (
	"sort"
	"strconv"
	"strings"

	"cloud.google.com/go/compute/apiv1/computepb"
)

func FirewallNeedsUpdate(fw *computepb.Firewall, expectedPorts map[int32]struct{}) bool {
	fw.GetAllowed()
	if fw == nil || fw.GetAllowed() == nil || len(fw.Allowed) != 1 {
		return true
	}
	rule := fw.Allowed[0]
	if rule == nil || len(rule.Ports) == 0 {
		return true
	}
	if rule.IPProtocol == nil || *rule.IPProtocol != "tcp" {
		return true
	}
	strPorts := toSortedStr(expectedPorts)
	portSet := map[string]struct{}{}
	for _, p := range strPorts {
		portSet[p] = struct{}{}
	}
	if len(rule.Ports) != len(portSet) {
		return true
	}
	for _, p := range rule.Ports {
		if _, ok := portSet[p]; !ok {
			return true
		}
	}
	return false
}

func ForwardingRuleFQN(project, region, name string) string {
	return regionFQNBase(project, region) + "/forwardingRules/" + name
}

func ServiceAttachmentFQN(project, region, name string) string {
	return regionFQNBase(project, region) + "/serviceAttachments/" + name
}

func regionFQNBase(project, region string) string {
	return "projects/" + project + "/regions/" + region
}

func toSortedStr(is map[int32]struct{}) []string {
	ss := make([]string, 0, len(is))
	for p, _ := range is {
		ss = append(ss, strconv.Itoa(int(p)))
	}
	sort.Slice(ss, func(i int, j int) bool { return ss[i] < ss[j] })
	return ss
}
