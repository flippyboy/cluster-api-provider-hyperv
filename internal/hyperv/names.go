package hyperv

import "strings"

const maxVMNameLen = 100

// VMName returns the Hyper-V VM name for a Cluster API machine.
// The cluster prefix keeps two workload clusters on one host apart.
// The result is at most 100 characters, which is the Hyper-V name limit.
func VMName(cluster, machine string) string {
	raw := "capi-" + cluster + "-" + machine
	var b strings.Builder
	prevHyphen := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' && (prevHyphen || b.Len() == 0) {
			continue
		}
		b.WriteRune(r)
		prevHyphen = r == '-'
	}
	name := strings.TrimRight(b.String(), "-")
	if name == "" {
		name = "capi-vm"
	}
	if len(name) > maxVMNameLen {
		name = strings.TrimRight(name[:maxVMNameLen], "-")
	}
	if name == "" {
		return "capi-vm"
	}
	return name
}

// JoinWin joins a Windows directory and a file name.
func JoinWin(dir, name string) string {
	dir = strings.TrimRight(dir, `\/`)
	if dir == "" {
		return name
	}
	return dir + `\` + name
}
