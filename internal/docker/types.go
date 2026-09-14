package docker

// ContainerSummary is an entry from GET /containers/json.
type ContainerSummary struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

// ContainerInspect is the response from GET /containers/{id}/json.
type ContainerInspect struct {
	ID              string             `json:"Id"`
	Name            string             `json:"Name"`
	Image           string             `json:"Image"`
	State           *ContainerState    `json:"State"`
	Config          *ContainerConfig   `json:"Config"`
	HostConfig      *HostConfigInspect `json:"HostConfig"`
	NetworkSettings *NetworkSettings   `json:"NetworkSettings"`
	SizeRw          int64              `json:"SizeRw"`
	SizeRootFs      int64              `json:"SizeRootFs"`
	Mounts          []MountPoint       `json:"Mounts"`
}

// HostConfigInspect is the host configuration block of an inspected container.
type HostConfigInspect struct {
	ExtraHosts  []string        `json:"ExtraHosts"`
	Binds       []string        `json:"Binds"`
	NetworkMode string          `json:"NetworkMode"`
	Devices     []deviceMapping `json:"Devices"`
	GroupAdd    []string        `json:"GroupAdd"`
	SecurityOpt []string        `json:"SecurityOpt"`
	IpcMode     string          `json:"IpcMode"`
}

// ContainerState is the runtime state block.
type ContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Paused     bool   `json:"Paused"`
	Restarting bool   `json:"Restarting"`
	ExitCode   int    `json:"ExitCode"`
}

// ContainerConfig is the immutable creation config.
type ContainerConfig struct {
	Image      string   `json:"Image"`
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	User       string   `json:"User"`
	WorkingDir string   `json:"WorkingDir"`
}

// NetworkSettings describes attached networks.
type NetworkSettings struct {
	IPAddress string                       `json:"IPAddress"`
	Networks  map[string]*EndpointSettings `json:"Networks"`
}

// EndpointSettings is a single network attachment.
type EndpointSettings struct {
	IPAddress string `json:"IPAddress"`
	NetworkID string `json:"NetworkID"`
}

// MountPoint describes an active mount in an inspected container.
type MountPoint struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

// Network is the response from GET /networks/{id}.
type Network struct {
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Containers map[string]struct {
		Name        string `json:"Name"`
		IPv4Address string `json:"IPv4Address"`
	} `json:"Containers"`
}

type networkCreateRequest struct {
	Name           string    `json:"Name"`
	Driver         string    `json:"Driver"`
	CheckDuplicate bool      `json:"CheckDuplicate"`
	IPAM           *ipamSpec `json:"IPAM,omitempty"`
}

type ipamSpec struct {
	Driver string          `json:"Driver"`
	Config []ipamIPAMEntry `json:"Config"`
}

type ipamIPAMEntry struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

type containerCreateRequest struct {
	Image      string      `json:"Image"`
	Cmd        []string    `json:"Cmd,omitempty"`
	Env        []string    `json:"Env,omitempty"`
	User       string      `json:"User,omitempty"`
	WorkingDir string      `json:"WorkingDir,omitempty"`
	Tty        bool        `json:"Tty"`
	OpenStdin  bool        `json:"OpenStdin"`
	HostConfig *hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	Binds       []string        `json:"Binds,omitempty"`
	Mounts      []mountSpec     `json:"Mounts,omitempty"`
	Devices     []deviceMapping `json:"Devices,omitempty"`
	GroupAdd    []string        `json:"GroupAdd,omitempty"`
	SecurityOpt []string        `json:"SecurityOpt,omitempty"`
	IpcMode     string          `json:"IpcMode,omitempty"`
	NetworkMode string          `json:"NetworkMode,omitempty"`
}

type mountSpec struct {
	Type     string `json:"Type"`
	Source   string `json:"Source"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly"`
}

type deviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type containerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type imageInspect struct {
	ID   string `json:"Id"`
	Size int64  `json:"Size"`
}

type execCreateRequest struct {
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	AttachStdin  bool     `json:"AttachStdin"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Env          []string `json:"Env,omitempty"`
	User         string   `json:"User,omitempty"`
}

type execCreateResponse struct {
	ID string `json:"Id"`
}

type execStartRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

type execInspectResponse struct {
	ID       string `json:"ID"`
	Running  bool   `json:"Running"`
	ExitCode int    `json:"ExitCode"`
}
