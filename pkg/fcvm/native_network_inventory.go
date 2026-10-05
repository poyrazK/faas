package fcvm

import (
	"fmt"
	"strconv"
	"strings"
)

func checkNativeNetworkInventory(leases []Lease, namespaces, interfaces []string) error {
	ownedNS, ownedLinks := make(map[string]bool), make(map[string]bool)
	for _, lease := range leases {
		if err := validateNativeJournalLease(lease); err != nil {
			return err
		}
		if lease.Networkless {
			continue
		}
		ownedNS[lease.Netns] = true
		ownedLinks[lease.VethHost], ownedLinks[lease.VethPeer] = true, true
		host, peer := privateVethNames(lease.Slot)
		ownedLinks[host], ownedLinks[peer] = true, true
	}
	for _, name := range namespaces {
		if strings.HasPrefix(name, "fc-") && !ownedNS[name] {
			return fmt.Errorf("native recovery: namespace %s has no held launch ownership", name)
		}
	}
	for _, name := range interfaces {
		if nativeLeaseInterfaceName(name) && !ownedLinks[name] {
			return fmt.Errorf("native recovery: interface %s has no held launch ownership", name)
		}
	}
	return nil
}

func nativeLeaseInterfaceName(name string) bool {
	for _, prefix := range []string{"vh", "vp", "gpn-h", "gpn-p"} {
		if suffix, ok := strings.CutPrefix(name, prefix); ok && suffix != "" {
			for _, digit := range suffix {
				if digit < '0' || digit > '9' {
					return false
				}
			}
			_, err := strconv.ParseUint(suffix, 10, 32)
			return err == nil
		}
	}
	return false
}
