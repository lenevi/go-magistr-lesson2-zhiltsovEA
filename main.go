package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

/*************** Validator ****************/
type Validator struct {
	file string
	errs int
}

func (v *Validator) fail(line int, msg string, args ...any) {
	fmt.Printf("%s:%d %s\n", v.file, line, fmt.Sprintf(msg, args...))
	v.errs++
}

func (v *Validator) requiredField(node *yaml.Node, field string) (*yaml.Node, bool) {
	m := mapify(node)
	val, ok := m[field]
	if !ok {
		v.fail(node.Line, "%s is required", field)
		return nil, false
	}
	return val, true
}

/*************** Helpers ****************/
func mapify(n *yaml.Node) map[string]*yaml.Node {
	res := make(map[string]*yaml.Node)
	if n.Kind != yaml.MappingNode {
		return res
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind == yaml.ScalarNode {
			res[k.Value] = v
		}
	}
	return res
}

func isInt(node *yaml.Node) bool {
	if node.Tag != "!!int" {
		return false
	}
	_, err := strconv.Atoi(node.Value)
	return err == nil
}

func isString(node *yaml.Node) bool {
	return node.Tag == "!!str"
}

var (
	reSnake  = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)
	reImage  = regexp.MustCompile(`^registry\.bigbrother\.io\/[^:\s]+:[^:\s]+$`)
	reMem    = regexp.MustCompile(`^[0-9]+(Gi|Mi|Ki)$`)
	reAbs    = regexp.MustCompile(`^/`)
	validOS  = map[string]bool{"linux": true, "windows": true}
	validPro = map[string]bool{"TCP": true, "UDP": true}
)

/*************** MAIN ****************/
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: yamlvalid <file>")
		os.Exit(2)
	}

	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
		os.Exit(2)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
		os.Exit(2)
	}

	v := &Validator{file: filepath.Base(path)}
	v.validateRoot(&root)

	if v.errs > 0 {
		os.Exit(1)
	}
}

/*************** Root ****************/
func (v *Validator) validateRoot(root *yaml.Node) {
	var doc *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	} else {
		doc = root
	}

	if doc.Kind != yaml.MappingNode {
		v.fail(doc.Line, "top-level must be a mapping")
		return
	}

	m := mapify(doc)

	// apiVersion
	api, ok := m["apiVersion"]
	if !ok {
		v.fail(doc.Line, "apiVersion is required")
	} else if !isString(api) {
		v.fail(api.Line, "apiVersion must be string")
	} else if api.Value != "v1" {
		v.fail(api.Line, "apiVersion has unsupported value '%s'", api.Value)
	}

	// kind
	kd, ok := m["kind"]
	if !ok {
		v.fail(doc.Line, "kind is required")
	} else if !isString(kd) {
		v.fail(kd.Line, "kind must be string")
	} else if kd.Value != "Pod" {
		v.fail(kd.Line, "kind has unsupported value '%s'", kd.Value)
	}

	// metadata
	meta, ok := m["metadata"]
	if !ok {
		v.fail(doc.Line, "metadata is required")
	} else {
		v.validateMetadata(meta)
	}

	// spec
	spec, ok := m["spec"]
	if !ok {
		v.fail(doc.Line, "spec is required")
	} else {
		v.validateSpec(spec)
	}
}

/*************** Metadata ****************/
func (v *Validator) validateMetadata(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "metadata must be object")
		return
	}
	m := mapify(node)

	// name
	nm, ok := m["name"]
	if !ok {
		v.fail(node.Line, "name is required")
	} else if !isString(nm) {
		v.fail(nm.Line, "name must be string")
	} else if strings.TrimSpace(nm.Value) == "" {
		v.fail(nm.Line, "name is required")
	}

	// namespace
	if ns, ok := m["namespace"]; ok {
		if !isString(ns) {
			v.fail(ns.Line, "namespace must be string")
		}
	}

	// labels
	if lbs, ok := m["labels"]; ok {
		if lbs.Kind != yaml.MappingNode {
			v.fail(lbs.Line, "labels must be object")
		} else {
			for i := 0; i+1 < len(lbs.Content); i += 2 {
				k := lbs.Content[i]
				val := lbs.Content[i+1]

				if k.Tag != "!!str" {
					v.fail(k.Line, "labels key must be string")
				}
				if val.Tag != "!!str" {
					v.fail(val.Line, "labels value must be string")
				}
			}
		}
	}
}

/*************** Spec ****************/
func (v *Validator) validateSpec(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "spec must be object")
		return
	}
	m := mapify(node)

	// os optional: scalar or object
	if osn, ok := m["os"]; ok {
		switch osn.Kind {
		case yaml.ScalarNode:
			if !validOS[osn.Value] {
				v.fail(osn.Line, "os has unsupported value '%s'", osn.Value)
			}
		case yaml.MappingNode:
			obj := mapify(osn)
			n, ok := obj["name"]
			if !ok {
				v.fail(osn.Line, "name is required")
			} else if !isString(n) {
				v.fail(n.Line, "name must be string")
			} else if !validOS[n.Value] {
				v.fail(n.Line, "name has unsupported value '%s'", n.Value)
			}
		default:
			v.fail(osn.Line, "os must be string or object")
		}
	}

	// containers required
	cn, ok := m["containers"]
	if !ok {
		v.fail(node.Line, "containers is required")
		return
	}
	if cn.Kind != yaml.SequenceNode {
		v.fail(cn.Line, "containers must be array")
		return
	}

	for _, item := range cn.Content {
		v.validateContainer(item)
	}
}

/*************** Container ****************/
func (v *Validator) validateContainer(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "container must be object")
		return
	}
	m := mapify(node)

	// name
	nm, ok := m["name"]
	if !ok {
		v.fail(node.Line, "name is required")
	} else if !isString(nm) {
		v.fail(nm.Line, "name must be string")
	} else if !reSnake.MatchString(nm.Value) {
		v.fail(nm.Line, "name has invalid format '%s'", nm.Value)
	}

	// image
	img, ok := m["image"]
	if !ok {
		v.fail(node.Line, "image is required")
	} else if !isString(img) {
		v.fail(img.Line, "image must be string")
	} else if !reImage.MatchString(img.Value) {
		v.fail(img.Line, "image has invalid format '%s'", img.Value)
	}

	// ports
	if prt, ok := m["ports"]; ok {
		if prt.Kind != yaml.SequenceNode {
			v.fail(prt.Line, "ports must be array")
		} else {
			for _, el := range prt.Content {
				v.validatePort(el)
			}
		}
	}

	// probes
	if rp, ok := m["readinessProbe"]; ok {
		v.validateProbe(rp)
	}
	if lp, ok := m["livenessProbe"]; ok {
		v.validateProbe(lp)
	}

	// resources
	res, ok := m["resources"]
	if !ok {
		v.fail(node.Line, "resources is required")
		return
	}
	v.validateResources(res)
}

/*************** ContainerPort ****************/
func (v *Validator) validatePort(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "ports item must be object")
		return
	}
	m := mapify(node)

	cp, ok := m["containerPort"]
	if !ok {
		v.fail(node.Line, "containerPort is required")
	} else if !isInt(cp) {
		v.fail(cp.Line, "containerPort must be int")
	} else {
		port, _ := strconv.Atoi(cp.Value)
		if port <= 0 || port >= 65536 {
			v.fail(cp.Line, "containerPort value out of range")
		}
	}

	if proto, ok := m["protocol"]; ok {
		if !isString(proto) {
			v.fail(proto.Line, "protocol must be string")
		} else if !validPro[proto.Value] {
			v.fail(proto.Line, "protocol has unsupported value '%s'", proto.Value)
		}
	}
}

/*************** Probe ****************/
func (v *Validator) validateProbe(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "readinessProbe must be object")
		return
	}
	m := mapify(node)

	hg, ok := m["httpGet"]
	if !ok {
		v.fail(node.Line, "httpGet is required")
		return
	}
	if hg.Kind != yaml.MappingNode {
		v.fail(hg.Line, "httpGet must be object")
		return
	}

	obj := mapify(hg)

	p, ok := obj["path"]
	if !ok {
		v.fail(hg.Line, "path is required")
	} else if !isString(p) {
		v.fail(p.Line, "path must be string")
	} else if !reAbs.MatchString(p.Value) {
		v.fail(p.Line, "path has invalid format '%s'", p.Value)
	}

	prt, ok := obj["port"]
	if !ok {
		v.fail(hg.Line, "port is required")
	} else if !isInt(prt) {
		v.fail(prt.Line, "port must be int")
	} else {
		x, _ := strconv.Atoi(prt.Value)
		if x <= 0 || x >= 65536 {
			v.fail(prt.Line, "port value out of range")
		}
	}
}

/*************** Resources ****************/
func (v *Validator) validateResources(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "resources must be object")
		return
	}
	m := mapify(node)

	if lim, ok := m["limits"]; ok {
		v.validateResKV("limits", lim)
	}
	if req, ok := m["requests"]; ok {
		v.validateResKV("requests", req)
	}
}

func (v *Validator) validateResKV(name string, node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		v.fail(node.Line, "%s must be object", name)
		return
	}
	m := mapify(node)

	if cpu, ok := m["cpu"]; ok {
		if !isInt(cpu) {
			v.fail(cpu.Line, "cpu must be int")
		}
	}
	if mem, ok := m["memory"]; ok {
		if !isString(mem) {
			v.fail(mem.Line, "memory must be string")
		} else if !reMem.MatchString(mem.Value) {
			v.fail(mem.Line, "memory has invalid format '%s'", mem.Value)
		}
	}
}
