package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type validationError struct {
	line *int  // nil, если строка неизвестна (например, обязательное поле отсутствует)
	msg  string
}

func (e *validationError) Error() string {
	return e.msg
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: yamlvalid <file>")
		os.Exit(1)
	}

	path := os.Args[1]
	if err := run(path); err != nil {
		if vErr, ok := err.(*validationError); ok {
			if vErr.line != nil {
				fmt.Fprintf(os.Stderr, "%s:%d %s\n", path, *vErr.line, vErr.msg)
			} else {
				fmt.Fprintf(os.Stderr, "%s: %s\n", path, vErr.msg)
			}
		} else {
			// системная ошибка чтения/парсинга
			fmt.Fprintln(os.Stderr, err.Error())
		}
		os.Exit(1)
	}

	// Успех — код 0, без вывода.
}

func run(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return fmt.Errorf("cannot unmarshal file content: %w", err)
	}

	if len(root.Content) == 0 {
		return &validationError{msg: "apiVersion is required"} // вообще нечего валидировать
	}

	doc := root.Content[0]
	return validatePod(doc)
}

func validatePod(doc *yaml.Node) error {
	if doc.Kind != yaml.MappingNode {
		return &validationError{
			line: &doc.Line,
			msg:  "root must be object",
		}
	}

	// --- Поля верхнего уровня ---

	// apiVersion (обязательно, string, значение v1)
	apiVerNode, ok := getMapValue(doc, "apiVersion")
	if !ok {
		return &validationError{msg: "apiVersion is required"}
	}
	if err := ensureString(apiVerNode, "apiVersion"); err != nil {
		return err
	}
	if apiVerNode.Value != "v1" {
		return &validationError{
			line: &apiVerNode.Line,
			msg:  fmt.Sprintf("apiVersion has unsupported value '%s'", apiVerNode.Value),
		}
	}

	// kind (обязательно, string, значение Pod)
	kindNode, ok := getMapValue(doc, "kind")
	if !ok {
		return &validationError{msg: "kind is required"}
	}
	if err := ensureString(kindNode, "kind"); err != nil {
		return err
	}
	if kindNode.Value != "Pod" {
		return &validationError{
			line: &kindNode.Line,
			msg:  fmt.Sprintf("kind has unsupported value '%s'", kindNode.Value),
		}
	}

	// metadata (ObjectMeta, обязательно)
	metaNode, ok := getMapValue(doc, "metadata")
	if !ok {
		return &validationError{msg: "metadata is required"}
	}
	if metaNode.Kind != yaml.MappingNode {
		return &validationError{
			line: &metaNode.Line,
			msg:  "metadata must be object",
		}
	}
	if err := validateMetadata(metaNode); err != nil {
		return err
	}

	// spec (PodSpec, обязательно)
	specNode, ok := getMapValue(doc, "spec")
	if !ok {
		return &validationError{msg: "spec is required"}
	}
	if specNode.Kind != yaml.MappingNode {
		return &validationError{
			line: &specNode.Line,
			msg:  "spec must be object",
		}
	}
	return validateSpec(specNode)
}

// --- ObjectMeta ---

func validateMetadata(node *yaml.Node) error {
	// name (обязательно, string)
	nameNode, ok := getMapValue(node, "name")
	if !ok {
		return &validationError{msg: "name is required"}
	}
	if err := ensureString(nameNode, "name"); err != nil {
		return err
	}

	// namespace (необязательно, string)
	if nsNode, ok := getMapValue(node, "namespace"); ok {
		if err := ensureString(nsNode, "namespace"); err != nil {
			return err
		}
	}

	// labels (необязательно, object)
	if labelsNode, ok := getMapValue(node, "labels"); ok {
		if labelsNode.Kind != yaml.MappingNode {
			return &validationError{
				line: &labelsNode.Line,
				msg:  "labels must be object",
			}
		}
		// Можно дополнительно проверить, что все ключи и значения — строки,
		// но задание этого явно не требует.
	}

	return nil
}

// --- PodSpec ---

func validateSpec(node *yaml.Node) error {
	// os: по примеру задания — просто строка: linux | windows
	if osNode, ok := getMapValue(node, "os"); ok {
		if err := ensureString(osNode, "os"); err != nil {
			return err
		}
		if osNode.Value != "linux" && osNode.Value != "windows" {
			return &validationError{
				line: &osNode.Line,
				msg:  fmt.Sprintf("os has unsupported value '%s'", osNode.Value),
			}
		}
	}

	// containers (обязательно, массив контейнеров)
	containersNode, ok := getMapValue(node, "containers")
	if !ok {
		return &validationError{msg: "containers is required"}
	}
	if containersNode.Kind != yaml.SequenceNode {
		return &validationError{
			line: &containersNode.Line,
			msg:  "containers must be array",
		}
	}
	if len(containersNode.Content) == 0 {
		// пустой список контейнеров, с точки зрения здравого смысла — ошибка
		return &validationError{
			line: &containersNode.Line,
			msg:  "containers value out of range",
		}
	}

	for _, c := range containersNode.Content {
		if c.Kind != yaml.MappingNode {
			return &validationError{
				line: &c.Line,
				msg:  "container must be object",
			}
		}
		if err := validateContainer(c); err != nil {
			return err
		}
	}

	return nil
}

// --- Container ---

func validateContainer(node *yaml.Node) error {
	// name (обязательно, snake_case)
	nameNode, ok := getMapValue(node, "name")
	if !ok {
		return &validationError{msg: "name is required"}
	}
	if err := ensureString(nameNode, "name"); err != nil {
		return err
	}
	if !isSnakeCase(nameNode.Value) {
		return &validationError{
			line: &nameNode.Line,
			msg:  fmt.Sprintf("name has invalid format '%s'", nameNode.Value),
		}
	}

	// image (обязательно; домен registry.bigbrother.io + тег)
	imageNode, ok := getMapValue(node, "image")
	if !ok {
		return &validationError{msg: "image is required"}
	}
	if err := ensureString(imageNode, "image"); err != nil {
		return err
	}
	if err := validateImage(imageNode); err != nil {
		return err
	}

	// ports (необязательно, массив объектов ContainerPort)
	if portsNode, ok := getMapValue(node, "ports"); ok {
		if portsNode.Kind != yaml.SequenceNode {
			return &validationError{
				line: &portsNode.Line,
				msg:  "ports must be array",
			}
		}
		for _, p := range portsNode.Content {
			if p.Kind != yaml.MappingNode {
				return &validationError{
					line: &p.Line,
					msg:  "ports must be object",
				}
			}
			if err := validateContainerPort(p); err != nil {
				return err
			}
		}
	}

	// readinessProbe (необязательно)
	if rpNode, ok := getMapValue(node, "readinessProbe"); ok {
		if rpNode.Kind != yaml.MappingNode {
			return &validationError{
				line: &rpNode.Line,
				msg:  "readinessProbe must be object",
			}
		}
		if err := validateProbe(rpNode); err != nil {
			return err
		}
	}

	// livenessProbe (необязательно)
	if lpNode, ok := getMapValue(node, "livenessProbe"); ok {
		if lpNode.Kind != yaml.MappingNode {
			return &validationError{
				line: &lpNode.Line,
				msg:  "livenessProbe must be object",
			}
		}
		if err := validateProbe(lpNode); err != nil {
			return err
		}
	}

	// resources (обязательно)
	resNode, ok := getMapValue(node, "resources")
	if !ok {
		return &validationError{msg: "resources is required"}
	}
	if resNode.Kind != yaml.MappingNode {
		return &validationError{
			line: &resNode.Line,
			msg:  "resources must be object",
		}
	}
	if err := validateResources(resNode); err != nil {
		return err
	}

	return nil
}

// --- ContainerPort ---

func validateContainerPort(node *yaml.Node) error {
	// containerPort (обязательно, int, диапазон)
	cpNode, ok := getMapValue(node, "containerPort")
	if !ok {
		return &validationError{msg: "containerPort is required"}
	}
	if err := ensureInt(cpNode, "containerPort"); err != nil {
		return err
	}
	val, _ := strconv.Atoi(cpNode.Value)
	if val <= 0 || val >= 65536 {
		return &validationError{
			line: &cpNode.Line,
			msg:  "containerPort value out of range",
		}
	}

	// protocol (необязательно, string, TCP/UDP)
	if protoNode, ok := getMapValue(node, "protocol"); ok {
		if err := ensureString(protoNode, "protocol"); err != nil {
			return err
		}
		if protoNode.Value != "TCP" && protoNode.Value != "UDP" {
			return &validationError{
				line: &protoNode.Line,
				msg:  fmt.Sprintf("protocol has unsupported value '%s'", protoNode.Value),
			}
		}
	}

	return nil
}

// --- Probe / HTTPGetAction ---

func validateProbe(node *yaml.Node) error {
	httpNode, ok := getMapValue(node, "httpGet")
	if !ok {
		return &validationError{msg: "httpGet is required"}
	}
	if httpNode.Kind != yaml.MappingNode {
		return &validationError{
			line: &httpNode.Line,
			msg:  "httpGet must be object",
		}
	}

	// path (обязательно, string, абсолютный путь)
	pathNode, ok := getMapValue(httpNode, "path")
	if !ok {
		return &validationError{msg: "path is required"}
	}
	if err := ensureString(pathNode, "path"); err != nil {
		return err
	}
	if !strings.HasPrefix(pathNode.Value, "/") {
		return &validationError{
			line: &pathNode.Line,
			msg:  fmt.Sprintf("path has invalid format '%s'", pathNode.Value),
		}
	}

	// port (обязательно, int, диапазон)
	portNode, ok := getMapValue(httpNode, "port")
	if !ok {
		return &validationError{msg: "port is required"}
	}
	if err := ensureInt(portNode, "port"); err != nil {
		return err
	}
	portVal, _ := strconv.Atoi(portNode.Value)
	if portVal <= 0 || portVal >= 65536 {
		return &validationError{
			line: &portNode.Line,
			msg:  "port value out of range",
		}
	}

	return nil
}

// --- ResourceRequirements ---

func validateResources(node *yaml.Node) error {
	// limits (необязательно, object)
	if limitsNode, ok := getMapValue(node, "limits"); ok {
		if limitsNode.Kind != yaml.MappingNode {
			return &validationError{
				line: &limitsNode.Line,
				msg:  "limits must be object",
			}
		}
		if err := validateResourceMap(limitsNode); err != nil {
			return err
		}
	}

	// requests (необязательно, object)
	if reqNode, ok := getMapValue(node, "requests"); ok {
		if reqNode.Kind != yaml.MappingNode {
			return &validationError{
				line: &reqNode.Line,
				msg:  "requests must be object",
			}
		}
		if err := validateResourceMap(reqNode); err != nil {
			return err
		}
	}

	return nil
}

func validateResourceMap(node *yaml.Node) error {
	for i := 0; i < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]

		switch k.Value {
		case "cpu":
			if err := ensureInt(v, "cpu"); err != nil {
				return err
			}
		case "memory":
			if err := ensureString(v, "memory"); err != nil {
				return err
			}
			if !isValidMemory(v.Value) {
				return &validationError{
					line: &v.Line,
					msg:  fmt.Sprintf("memory has invalid format '%s'", v.Value),
				}
			}
		default:
			// неизвестный ресурс можно игнорировать или тоже валидировать;
			// задание этого не требует.
		}
	}
	return nil
}

// --- Вспомогательные функции ---

func getMapValue(m *yaml.Node, key string) (*yaml.Node, bool) {
	if m.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		v := m.Content[i+1]
		if k.Value == key {
			return v, true
		}
	}
	return nil, false
}

func ensureString(node *yaml.Node, field string) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return &validationError{
			line: &node.Line,
			msg:  fmt.Sprintf("%s must be string", field),
		}
	}
	return nil
}

func ensureInt(node *yaml.Node, field string) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return &validationError{
			line: &node.Line,
			msg:  fmt.Sprintf("%s must be int", field),
		}
	}
	if _, err := strconv.Atoi(node.Value); err != nil {
		return &validationError{
			line: &node.Line,
			msg:  fmt.Sprintf("%s must be int", field),
		}
	}
	return nil
}

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' && i != 0 && i != len(s)-1:
		default:
			return false
		}
	}
	return true
}

func validateImage(node *yaml.Node) error {
	val := node.Value
	if !strings.HasPrefix(val, "registry.bigbrother.io/") {
		return &validationError{
			line: &node.Line,
			msg:  fmt.Sprintf("image has invalid format '%s'", val),
		}
	}
	slash := strings.LastIndex(val, "/")
	colon := strings.LastIndex(val, ":")
	if colon == -1 || colon < slash+1 || colon == len(val)-1 {
		return &validationError{
			line: &node.Line,
			msg:  fmt.Sprintf("image has invalid format '%s'", val),
		}
	}
	return nil
}

func isValidMemory(s string) bool {
	// формат: <целое_число><Gi|Mi|Ki>
	if len(s) < 3 {
		return false
	}
	unit := s[len(s)-2:]
	if unit != "Gi" && unit != "Mi" && unit != "Ki" {
		return false
	}
	numPart := s[:len(s)-2]
	if numPart == "" {
		return false
	}
	_, err := strconv.Atoi(numPart)
	return err == nil
}
