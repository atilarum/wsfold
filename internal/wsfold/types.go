package wsfold

import (
	"fmt"
	"strings"
)

type TrustClass string
type CompletionSource string
type AttachmentBackend string
type WorktreeControlMode string
type ManagedWorktreeOwner string
type RealizationStatus string

const (
	TrustClassTrusted      TrustClass       = "trusted"
	TrustClassExternal     TrustClass       = "external"
	CompletionSourceLocal  CompletionSource = "local"
	CompletionSourceRemote CompletionSource = "remote"

	AttachmentBackendSymlink         AttachmentBackend = "symlink"
	AttachmentBackendLinuxNativeBind AttachmentBackend = "linux-native-bind"
	AttachmentBackendLinuxFuseBind   AttachmentBackend = "linux-fuse-bind"
	AttachmentBackendMacOSFuseBind   AttachmentBackend = "macos-fuse-bind"

	WorktreeControlWorkspaceMountedPrimary WorktreeControlMode  = "workspace-mounted-primary"
	ManagedWorktreeOwnerWSFold             ManagedWorktreeOwner = "wsfold"

	RealizationAttached  RealizationStatus = "attached"
	RealizationUnmounted RealizationStatus = "unmounted"
	RealizationInvalid   RealizationStatus = "invalid"
)

type Repo struct {
	LocalName    string
	Name         string
	Slug         string
	Branch       string
	IsWorktree   bool
	CheckoutPath string
	OriginURL    string
	TrustClass   TrustClass
}

func (r Repo) DisplayRef() string {
	if r.Slug != "" && !r.IsWorktree {
		return r.Slug
	}
	if r.Slug != "" && r.IsWorktree && strings.TrimSpace(r.Branch) != "" {
		return r.Slug + "/" + strings.TrimSpace(r.Branch)
	}
	if r.LocalName != "" {
		return r.LocalName
	}
	if r.Name != "" {
		return r.Name
	}
	return r.CheckoutPath
}

type Entry struct {
	RepoRef          string            `yaml:"repo_ref" json:"repo_ref"`
	CheckoutPath     string            `yaml:"checkout_path" json:"checkout_path"`
	TrustClass       TrustClass        `yaml:"trust_class" json:"trust_class"`
	Backend          AttachmentBackend `yaml:"backend,omitempty" json:"backend,omitempty"`
	MountPath        string            `yaml:"mount_path,omitempty" json:"mount_path,omitempty"`
	ResolutionDetail string            `yaml:"-" json:"-"`
	CacheInferred    bool              `yaml:"-" json:"-"`
	CachePresent     bool              `yaml:"-" json:"-"`
	CachedCheckout   string            `yaml:"-" json:"-"`
	CachedBackend    AttachmentBackend `yaml:"-" json:"-"`
}

func (e Entry) Key() string {
	return fmt.Sprintf("%s|%s|%s", e.TrustClass, e.RepoRef, e.CheckoutPath)
}

type ManagedWorktreeEntry struct {
	RepoRef             string               `yaml:"repo_ref" json:"repo_ref"`
	Branch              string               `yaml:"branch" json:"branch"`
	WorkspacePath       string               `yaml:"workspace_path" json:"workspace_path"`
	PrimaryRepoRef      string               `yaml:"primary_repo_ref" json:"primary_repo_ref"`
	PrimaryCheckoutPath string               `yaml:"primary_checkout_path" json:"primary_checkout_path"`
	PrimaryMountPath    string               `yaml:"primary_mount_path" json:"primary_mount_path"`
	ControlMode         WorktreeControlMode  `yaml:"control_mode" json:"control_mode"`
	Owner               ManagedWorktreeOwner `yaml:"owner" json:"owner"`
	CreationSource      string               `yaml:"creation_source" json:"creation_source"`
	UnsupportedLegacy   bool                 `yaml:"unsupported_legacy,omitempty" json:"unsupported_legacy,omitempty"`
}

func (e ManagedWorktreeEntry) Key() string {
	return fmt.Sprintf("managed-worktree|%s|%s", e.PrimaryRepoRef, e.WorkspacePath)
}

type WorkspaceManifest struct {
	SchemaVersion int                     `yaml:"schema_version"`
	Trusted       []TrustedManifestEntry  `yaml:"trusted,omitempty"`
	External      []ExternalManifestEntry `yaml:"external,omitempty"`
	Worktrees     []WorktreeManifestEntry `yaml:"worktrees,omitempty"`
}

type TrustedManifestEntry struct {
	Ref  string `yaml:"ref"`
	Path string `yaml:"path"`
}

type ExternalManifestEntry struct {
	Ref string `yaml:"ref"`
}

type WorktreeManifestEntry struct {
	Of     string `yaml:"of"`
	Branch string `yaml:"branch"`
	Path   string `yaml:"path"`
}

type WorkspaceCache struct {
	SchemaVersion int                  `yaml:"schema_version"`
	Trusted       []TrustedCacheEntry  `yaml:"trusted,omitempty"`
	External      []ExternalCacheEntry `yaml:"external,omitempty"`
}

type TrustedCacheEntry struct {
	Ref          string            `yaml:"ref"`
	CheckoutPath string            `yaml:"checkout_path"`
	Backend      AttachmentBackend `yaml:"backend"`
}

type ExternalCacheEntry struct {
	Ref          string `yaml:"ref"`
	CheckoutPath string `yaml:"checkout_path"`
}
