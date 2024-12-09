package buildflags

import (
	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
)

type Ulimits map[string]*Ulimit

func ParseUlimits(s []string) (Ulimits, error) {
	ulimits := make(map[string]*Ulimit)
	for _, in := range s {
		u, err := units.ParseUlimit(in)
		if err != nil {
			return nil, err
		}
		ulimits[u.Name] = &Ulimit{
			Soft: u.Soft,
			Hard: u.Hard,
		}
	}
	return ulimits, nil
}

func (u Ulimits) Merge(other Ulimits) Ulimits {
	if u == nil {
		u = make(map[string]*Ulimit)
	}
	for k, v := range other {
		u[k] = v
	}
	return u
}

func (u Ulimits) ToUlimitOpt() *dockeropts.UlimitOpt {
	ref := make(map[string]*container.Ulimit, len(u))
	for name, ulimit := range u {
		ref[name] = &container.Ulimit{
			Name: name,
			Soft: ulimit.Soft,
			Hard: ulimit.Hard,
		}
	}
	return dockeropts.NewUlimitOpt(&ref)
}

func (u Ulimits) String() string {
	return u.ToUlimitOpt().String()
}

type Ulimit struct {
	Hard int64 `json:"hard"`
	Soft int64 `json:"soft"`
}
