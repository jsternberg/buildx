package build

import (
	"github.com/docker/buildx/controller/pb"
	sourcepolicy "github.com/moby/buildkit/sourcepolicy/pb"
)

type Options struct {
	ContextPath            string
	DockerfileName         string
	CallFunc               *pb.CallFunc
	NamedContexts          map[string]string
	Allow                  []string
	Attests                []*pb.Attest
	BuildArgs              map[string]string
	CacheFrom              []*pb.CacheOptionsEntry
	CacheTo                []*pb.CacheOptionsEntry
	CgroupParent           string
	Exports                []*pb.ExportEntry
	ExtraHosts             []string
	Labels                 map[string]string
	NetworkMode            string
	NoCacheFilter          []string
	Platforms              []string
	Secrets                []*pb.Secret
	ShmSize                int64
	SSH                    []*pb.SSH
	Tags                   []string
	Target                 string
	Ulimits                *pb.UlimitOpt
	Builder                string
	NoCache                bool
	Pull                   bool
	ExportPush             bool
	ExportLoad             bool
	SourcePolicy           *sourcepolicy.Policy
	Ref                    string
	GroupRef               string
	Annotations            []string
	ProvenanceResponseMode string
}
