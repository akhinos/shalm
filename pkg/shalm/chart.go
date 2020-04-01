package shalm

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/blang/semver"
	"go.starlark.net/starlark"
)

type chartImpl struct {
	clazz     chartClass
	Version   semver.Version
	values    starlark.StringDict
	methods   map[string]starlark.Callable
	dir       string
	namespace string
	suffix    string
	initFunc  *starlark.Function
}

var (
	_ ChartValue = (*chartImpl)(nil)
)

func newChart(thread *starlark.Thread, repo Repo, dir string, opts ...ChartOption) (*chartImpl, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	name := strings.Split(filepath.Base(abs), ":")[0]
	co := chartOptions(opts)
	c := &chartImpl{dir: dir, namespace: co.namespace, suffix: co.suffix, clazz: chartClass{Name: name}}
	c.values = make(map[string]starlark.Value)
	c.methods = make(map[string]starlark.Callable)
	hasChartYaml := false
	if err := c.loadChartYaml(); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		hasChartYaml = true
	}
	if err := c.loadYaml("values.yaml"); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		hasChartYaml = true
	}
	if err := c.init(thread, repo, hasChartYaml, co.args, co.kwargs); err != nil {
		return nil, err
	}
	return c, nil

}

func (c *chartImpl) builtin(name string, fn func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error)) starlark.Callable {
	return starlark.NewBuiltin(name+" at "+path.Join(c.dir, "Chart.star"), fn)
}

func (c *chartImpl) GetName() string {
	if c.suffix == "" {
		return c.clazz.Name
	}
	return fmt.Sprintf("%s-%s", c.clazz.Name, c.suffix)
}

func (c *chartImpl) GetVersion() semver.Version {
	return c.clazz.GetVersion()
}

func (c *chartImpl) GetVersionString() string {
	return c.clazz.Version
}

func (c *chartImpl) walk(cb func(name string, size int64, body io.Reader, err error) error) error {
	return filepath.Walk(c.dir, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(c.dir, file)
		if err != nil {
			return err
		}
		body, err := os.Open(file)
		if err != nil {
			return err
		}
		defer body.Close()
		return cb(rel, info.Size(), body, nil)
	})
}

func (c *chartImpl) path(part ...string) string {
	return filepath.Join(append([]string{c.dir}, part...)...)
}

func (c *chartImpl) String() string {
	buf := new(strings.Builder)
	buf.WriteString("chart")
	buf.WriteByte('(')
	s := 0
	for i, e := range c.values {
		if s > 0 {
			buf.WriteString(", ")
		}
		s++
		buf.WriteString(i)
		buf.WriteString(" = ")
		buf.WriteString(e.String())
	}
	buf.WriteByte(')')
	return buf.String()
}

// Type -
func (c *chartImpl) Type() string { return "chart" }

// Truth -
func (c *chartImpl) Truth() starlark.Bool { return true } // even when empty

// Hash -
func (c *chartImpl) Hash() (uint32, error) {
	var x, m uint32 = 8731, 9839
	for k, e := range c.values {
		namehash, _ := starlark.String(k).Hash()
		x = x ^ 3*namehash
		y, err := e.Hash()
		if err != nil {
			return 0, err
		}
		x = x ^ y*m
		m += 7349
	}
	return x, nil
}

// Freeze -
func (c *chartImpl) Freeze() {
}

// Attr returns the value of the specified field.
func (c *chartImpl) Attr(name string) (starlark.Value, error) {
	if name == "namespace" {
		return starlark.String(c.namespace), nil
	}
	if name == "name" {
		return starlark.String(c.GetName()), nil
	}
	if name == "__class__" {
		return &c.clazz, nil
	}
	value, ok := c.values[name]
	if !ok {
		var m starlark.Value
		m, ok = c.methods[name]
		if !ok {
			m = nil
		}
		if m == nil {
			return nil, starlark.NoSuchAttrError(
				fmt.Sprintf("chart has no .%s attribute", name))

		}
		return m, nil
	}
	return wrapDict(value), nil
}

// AttrNames returns a new sorted list of the struct fields.
func (c *chartImpl) AttrNames() []string {
	names := make([]string, 0)
	for k := range c.values {
		names = append(names, k)
	}
	names = append(names, "template")
	return names
}

// SetField -
func (c *chartImpl) SetField(name string, val starlark.Value) error {
	c.values[name] = unwrapDict(val)
	return nil
}

func (c *chartImpl) applyFunction() starlark.Callable {
	return c.builtin("apply", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, e error) {
		var k K8sValue
		if err := starlark.UnpackArgs("apply", args, kwargs, "k8s", &k); err != nil {
			return nil, err
		}
		return starlark.None, c.apply(thread, k)
	})
}

func (c *chartImpl) Apply(thread *starlark.Thread, k K8s) error {
	_, err := starlark.Call(thread, c.methods["apply"], starlark.Tuple{NewK8sValue(k)}, nil)
	if err != nil {
		return err
	}
	k.Progress(100)
	return nil
}

func (c *chartImpl) apply(thread *starlark.Thread, k K8sValue) error {
	err := c.eachSubChart(func(subChart *chartImpl) error {
		_, err := starlark.Call(thread, subChart.methods["apply"], starlark.Tuple{k}, nil)
		return err
	})
	if err != nil {
		return err
	}
	return c.applyLocal(thread, k, &K8sOptions{}, "")
}

func (c *chartImpl) applyLocalFunction() starlark.Callable {
	return c.builtin("__apply", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, e error) {
		var k K8sValue
		parser := &kwargsParser{kwargs: kwargs}
		var glob string
		k8sOptions := unpackK8sOptions(parser)
		if err := starlark.UnpackArgs("__apply", args, parser.Parse(), "k8s", &k, "glob?", &glob); err != nil {
			return nil, err
		}
		return starlark.None, c.applyLocal(thread, k, k8sOptions, glob)
	})
}

func (c *chartImpl) applyLocal(thread *starlark.Thread, k K8sValue, k8sOptions *K8sOptions, glob string) error {
	err := c.eachVault(func(v *vault) error {
		return v.read(k)
	})
	if err != nil {
		return err
	}
	k8sOptions.Namespaced = false
	return k.Apply(decode(c.template(thread, glob, true)), k8sOptions)
}

func (c *chartImpl) Delete(thread *starlark.Thread, k K8s) error {
	_, err := starlark.Call(thread, c.methods["delete"], starlark.Tuple{NewK8sValue(k)}, nil)
	if err != nil {
		return err
	}
	k.Progress(100)
	return nil
}

func (c *chartImpl) deleteFunction() starlark.Callable {
	return c.builtin("delete", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, e error) {
		var k K8sValue
		if err := starlark.UnpackArgs("delete", args, kwargs, "k8s", &k); err != nil {
			return nil, err
		}
		return starlark.None, c.delete(thread, k)
	})
}

func (c *chartImpl) delete(thread *starlark.Thread, k K8sValue) error {
	err := c.eachSubChart(func(subChart *chartImpl) error {
		_, err := starlark.Call(thread, subChart.methods["delete"], starlark.Tuple{k}, nil)
		return err
	})
	if err != nil {
		return err
	}
	return c.deleteLocal(thread, k, &K8sOptions{}, "")
}

func (c *chartImpl) deleteLocalFunction() starlark.Callable {
	return c.builtin("__delete", func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (value starlark.Value, e error) {
		var k K8sValue
		var glob string
		parser := &kwargsParser{kwargs: kwargs}
		k8sOptions := unpackK8sOptions(parser)
		if err := starlark.UnpackArgs("__delete", args, parser.Parse(), "k8s", &k, "glob?", &glob); err != nil {
			return nil, err
		}
		return starlark.None, c.deleteLocal(thread, k, k8sOptions, glob)
	})
}

func (c *chartImpl) deleteLocal(thread *starlark.Thread, k K8sValue, k8sOptions *K8sOptions, glob string) error {
	k8sOptions.Namespaced = false
	err := k.Delete(decode(c.template(thread, glob, false)), k8sOptions)
	if err != nil {
		return err
	}
	return c.eachVault(func(v *vault) error {
		return v.delete()
	})
}

func (c *chartImpl) eachSubChart(block func(subChart *chartImpl) error) error {
	for _, v := range c.values {
		subChart, ok := v.(*chartImpl)
		if ok {
			err := block(subChart)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *chartImpl) eachVault(block func(x *vault) error) error {
	for _, val := range c.values {
		v, ok := val.(*vault)
		if ok {
			err := block(v)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *chartImpl) mergeValues(values map[string]interface{}) {
	for k, v := range values {
		c.values[k] = merge(c.values[k], toStarlark(v))
	}
}