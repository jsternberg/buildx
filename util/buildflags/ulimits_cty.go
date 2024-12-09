package buildflags

import (
	"strconv"
	"strings"
	"sync"

	"github.com/docker/go-units"
	"github.com/pkg/errors"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

var ulimitType = sync.OnceValue(func() cty.Type {
	return cty.ObjectWithOptionalAttrs(map[string]cty.Type{
		"soft": cty.Number,
		"hard": cty.Number,
	}, []string{"hard"})
})

func (u *Ulimits) FromCtyValue(in cty.Value, p cty.Path) error {
	if in.Type().IsListType() || in.Type().IsTupleType() {
		return u.fromCtyList(in, p)
	}

	if in.Type().IsMapType() || in.Type().IsObjectType() {
		return u.fromCtyMap(in, p)
	}
	return p.NewErrorf("%s", convert.MismatchMessage(in.Type(), cty.Map(ulimitType())))
}

func (u *Ulimits) fromCtyList(in cty.Value, p cty.Path) error {
	vals := make(map[string]*Ulimit, in.LengthInt())
	for itr := in.ElementIterator(); itr.Next(); {
		_, val := itr.Element()

		if val.Type() != cty.String {
			return p.NewErrorf("%s", convert.MismatchMessage(val.Type(), cty.String))
		}

		ulimit, err := units.ParseUlimit(val.AsString())
		if err != nil {
			return err
		}
		vals[ulimit.Name] = &Ulimit{
			Soft: ulimit.Soft,
			Hard: ulimit.Hard,
		}
	}
	*u = vals
	return nil
}

func (u *Ulimits) fromCtyMap(in cty.Value, p cty.Path) error {
	vals := make(map[string]*Ulimit, in.LengthInt())
	for itr := in.ElementIterator(); itr.Next(); {
		key, val := itr.Element()

		ulimit := &Ulimit{}
		if err := ulimit.FromCtyValue(val, p); err != nil {
			return err
		}
		vals[key.AsString()] = ulimit
	}
	*u = vals
	return nil
}

func (u Ulimits) ToCtyValue() cty.Value {
	if len(u) == 0 {
		return cty.MapValEmpty(ulimitType())
	}

	vals := make(map[string]cty.Value, len(u))
	for name, ulimit := range u {
		vals[name] = ulimit.ToCtyValue()
	}
	return cty.MapVal(vals)
}

func (u *Ulimit) FromCtyValue(in cty.Value, p cty.Path) error {
	inType := in.Type()
	switch inType {
	case cty.Number:
		return u.fromCtyNumber(in)
	case cty.String:
		return u.fromCtyString(in)
	}

	if inType.IsListType() || inType.IsTupleType() {
		return u.fromCtyList(in)
	}

	// Use the object form as the canonical.
	// TODO: maybe we should make a more complex error message?
	// The object syntax is nice for referencing values, but it's
	// a bit awkward to actually use.
	return u.fromCtyObject(in)
}

func (u *Ulimit) fromCtyNumber(in cty.Value) error {
	u.Soft, _ = in.AsBigFloat().Int64()
	u.Hard = u.Soft
	return nil
}

func (u *Ulimit) fromCtyString(in cty.Value) error {
	parts := strings.Split(in.AsString(), ":")
	elems := make([]cty.Value, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return err
		}
		elems[i] = cty.NumberIntVal(v)
	}
	return u.fromCtyNumberList(cty.ListVal(elems))
}

func (u *Ulimit) fromCtyList(in cty.Value) error {
	wantType := cty.List(cty.Number)
	if !in.Type().Equals(wantType) {
		// Try to convert.
		want, err := convert.Convert(in, wantType)
		if err != nil {
			return err
		}
		in = want
	}
	return u.fromCtyNumberList(in)
}

func (u *Ulimit) fromCtyNumberList(in cty.Value) error {
	// Check the length of the list.
	n := in.LengthInt()
	if n < 1 || n > 2 {
		parts := make([]int64, 0, n)
		for itr := in.ElementIterator(); itr.Next(); {
			_, elem := itr.Element()
			v, _ := elem.AsBigFloat().Int64()
			parts = append(parts, v)
		}
		if n < 1 {
			return errors.Errorf("too few limit value arguments - %+v, must have at least one, `soft[:hard]`", parts)
		} else if n > 2 {
			return errors.Errorf("too many limit value arguments - %+v, can only have up to two, `soft[:hard]`", parts)
		}
	}

	itr := in.ElementIterator()
	itr.Next()

	_, soft := itr.Element()
	u.Soft, _ = soft.AsBigFloat().Int64()

	if itr.Next() {
		_, hard := itr.Element()
		u.Hard, _ = hard.AsBigFloat().Int64()
	} else {
		u.Hard = u.Soft
	}
	return nil
}

func (u *Ulimit) fromCtyObject(in cty.Value) error {
	v, err := convert.Convert(in, ulimitType())
	if err != nil {
		return err
	}

	u.Soft, _ = v.GetAttr("soft").AsBigFloat().Int64()
	if hard := v.GetAttr("hard"); !hard.IsNull() {
		u.Hard, _ = hard.AsBigFloat().Int64()
	} else {
		u.Hard = u.Soft
	}
	return nil
}

func (u *Ulimit) ToCtyValue() cty.Value {
	if u == nil {
		return cty.NullVal(cty.Map(ulimitType()))
	}

	return cty.ObjectVal(map[string]cty.Value{
		"hard": cty.NumberIntVal(u.Hard),
		"soft": cty.NumberIntVal(u.Soft),
	})
}
