package shalm

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.starlark.net/starlark"
	corev1 "k8s.io/api/core/v1"
)

// JewelBackend -
type JewelBackend interface {
	Name() string
	Keys() map[string]string
	Apply(map[string][]byte) (map[string][]byte, error)
}

// ComplexJewelBackend -
type ComplexJewelBackend interface {
	JewelBackend
	Template() (map[string][]byte, error)
	Delete() error
}

const stateInit = 0
const stateLoaded = 1
const stateReady = 2

type vaultK8s struct {
	k8s          K8sReader
	objectWriter ObjectWriter
	namespace    string
}

// Vault -
type Vault interface {
	Write(name string, data map[string][]byte) error
	Read(name string) (map[string][]byte, error)
	IsNotExist(err error) bool
}

func (v *vaultK8s) Write(name string, data map[string][]byte) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return v.objectWriter(&Object{
		APIVersion: corev1.SchemeGroupVersion.String(),
		Kind:       "Secret",
		MetaData: MetaData{
			Name:      name,
			Namespace: v.namespace,
		},
		Additional: map[string]json.RawMessage{
			"type": json.RawMessage([]byte(`"Opaque"`)),
			"data": json.RawMessage(dataJSON),
		},
	})
}

func (v *vaultK8s) Read(name string) (map[string][]byte, error) {
	obj, err := v.k8s.Get("secret", name, &K8sOptions{Namespaced: true})
	if err != nil {
		return nil, err
	}
	var data map[string][]byte
	if err := json.Unmarshal(obj.Additional["data"], &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (v *vaultK8s) IsNotExist(err error) bool {
	return v.k8s.IsNotExist(err)
}

type jewel struct {
	backend JewelBackend
	state   int
	name    string
	data    map[string][]byte
}

var (
	_ starlark.Value = (*jewel)(nil)
)

// NewJewel -
func NewJewel(backend JewelBackend, name string) (starlark.Value, error) {
	return &jewel{
		backend: backend,
		name:    name,
		data:    map[string][]byte{},
	}, nil
}

func (c *jewel) delete() error {
	complex, ok := c.backend.(ComplexJewelBackend)
	if ok {
		return complex.Delete()
	}
	return nil
}

// String -
func (c *jewel) String() string {
	buf := new(strings.Builder)
	buf.WriteString(c.backend.Name())
	buf.WriteByte('(')
	buf.WriteString("name = ")
	buf.WriteString(c.name)
	buf.WriteByte(')')
	return buf.String()
}

func (c *jewel) read(v Vault) error {
	data, err := v.Read(c.name)
	if err != nil {
		if !v.IsNotExist(err) {
			return err
		}
	} else {
		c.data = data
		c.state = stateLoaded
	}
	return nil
}

func (c *jewel) write(v Vault) error {
	return v.Write(c.name, c.data)
}

func (c *jewel) ensure() (err error) {
	var data map[string][]byte
	switch c.state {
	case stateLoaded:
		data, err = c.backend.Apply(c.data)
		if err != nil {
			return
		}
	case stateInit:
		complex, ok := c.backend.(ComplexJewelBackend)
		if ok {
			data, err = complex.Template()
		} else {
			data, err = c.backend.Apply(make(map[string][]byte))
		}
		if err != nil {
			return
		}
	case stateReady:
		return nil
	}
	c.data = data
	c.state = stateReady
	return nil
}

func (c *jewel) templateValues() map[string]string {
	_ = c.ensure()
	result := map[string]string{}
	for k, v := range c.backend.Keys() {
		result[k] = dataValue(c.data[v])
	}
	return result
}

func dataValue(data []byte) string {
	if data != nil {
		return string(data)
	} else {
		return ""
	}
}

// Type -
func (c *jewel) Type() string { return c.backend.Name() }

// Freeze -
func (c *jewel) Freeze() {}

// Truth -
func (c *jewel) Truth() starlark.Bool { return false }

// Hash -
func (c *jewel) Hash() (uint32, error) { panic("implement me") }

// Attr -
func (c *jewel) Attr(name string) (starlark.Value, error) {
	if name == "name" {
		return starlark.String(c.name), nil
	}
	key, ok := c.backend.Keys()[name]
	if !ok {
		return starlark.None, starlark.NoSuchAttrError(fmt.Sprintf("%s has no .%s attribute", c.backend.Name(), name))
	}
	err := c.ensure()
	if err != nil {
		return starlark.None, err
	}
	return starlark.String(dataValue(c.data[key])), nil
}

// AttrNames -
func (c *jewel) AttrNames() []string {
	result := []string{"name"}
	for k := range c.backend.Keys() {
		result = append(result, k)
	}
	return result
}
