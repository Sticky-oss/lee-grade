package main

// --hosts support: load a small YAML map of logical managed-node names to SSH
// connection details, so checks carrying a `host:` grade that node remotely.
//
// hosts.yaml shape (as written by labs/multinode-setup.sh):
//
//	hosts:
//	  node1:
//	    address: 10.89.0.4
//	    user: ansible
//	    key: /home/lee/multinode/ansible_node
//	  node2:
//	    address: 10.89.0.5
//	    user: ansible
//	    key: /home/lee/multinode/ansible_node

import (
	"fmt"
	"os"

	"github.com/sticky-oss/lee-grade/internal/check"
	"gopkg.in/yaml.v3"
)

type hostsFile struct {
	Hosts map[string]struct {
		Address string `yaml:"address"`
		User    string `yaml:"user"`
		Key     string `yaml:"key"`
		Port    int    `yaml:"port"`
	} `yaml:"hosts"`
}

// loadHosts reads a --hosts file and registers it with the check engine.
func loadHosts(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read hosts file %s: %w", path, err)
	}
	var hf hostsFile
	if err := yaml.Unmarshal(data, &hf); err != nil {
		return fmt.Errorf("parse hosts file %s: %w", path, err)
	}
	if len(hf.Hosts) == 0 {
		return fmt.Errorf("hosts file %s defines no hosts", path)
	}
	m := make(map[string]check.HostSpec, len(hf.Hosts))
	for name, h := range hf.Hosts {
		if h.Address == "" {
			return fmt.Errorf("host %q in %s is missing 'address'", name, path)
		}
		m[name] = check.HostSpec{Address: h.Address, User: h.User, Key: h.Key, Port: h.Port}
	}
	check.SetHosts(m)
	return nil
}
