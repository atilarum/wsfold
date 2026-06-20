package wsfold

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	skillbundle "github.com/atilarum/wsfold"
)

var wsfoldAgentSkillNames = []string{"wsfold"}

var createAgentSkillSymlink = os.Symlink

type InitOptions struct {
	NoSkills      bool
	RefreshSkills bool
}

func (a *App) InitWithOptions(cwd string, opts InitOptions) error {
	if opts.NoSkills && opts.RefreshSkills {
		return fmt.Errorf("--no-skills and --refresh-skills cannot be used together")
	}

	primaryRoot, err := currentWorkspaceRoot(cwd)
	if err != nil {
		return err
	}
	if _, err := os.Stat(manifestPath(primaryRoot)); err == nil {
		if err := a.installLocalAgentSkills(primaryRoot, opts); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(a.Stdout, "already initialized %s\n", primaryRoot)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect manifest: %w", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	manifest := Manifest{
		Version:          manifestVersion,
		PrimaryRoot:      primaryRoot,
		Trusted:          []Entry{},
		External:         []Entry{},
		ManagedWorktrees: []ManagedWorktreeEntry{},
	}

	if err := saveManifest(primaryRoot, manifest); err != nil {
		return err
	}
	if err := ensureCacheIgnored(primaryRoot); err != nil {
		return err
	}
	if err := writeWorkspace(primaryRoot, Manifest{}, manifest, cfg.ProjectsDirName); err != nil {
		return err
	}
	if err := a.installLocalAgentSkills(primaryRoot, opts); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(a.Stdout, "initialized %s\n", primaryRoot)
	return nil
}

func (a *App) installLocalAgentSkills(primaryRoot string, opts InitOptions) error {
	if opts.NoSkills {
		return nil
	}
	for _, name := range wsfoldAgentSkillNames {
		destination := filepath.Join(primaryRoot, ".agents", "skills", name)
		if err := installCanonicalAgentSkill(name, destination, opts.RefreshSkills); err != nil {
			return err
		}
	}
	for _, name := range wsfoldAgentSkillNames {
		canonical := filepath.Join(primaryRoot, ".agents", "skills", name)
		claude := filepath.Join(primaryRoot, ".claude", "skills", name)
		if err := installClaudeAgentSkill(name, canonical, claude, opts.RefreshSkills); err != nil {
			return err
		}
	}
	return nil
}

func installCanonicalAgentSkill(name string, destination string, refresh bool) error {
	if refresh {
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("refresh local skill %s: %w", name, err)
		}
	} else if exists, usable, err := localSkillPathState(destination); err != nil {
		return err
	} else if exists && usable {
		return nil
	} else if exists {
		return fmt.Errorf("local skill path %s exists but does not contain SKILL.md; use --refresh-skills to replace it", destination)
	}
	if err := copyEmbeddedSkillDir(name, destination); err != nil {
		return fmt.Errorf("install local skill %s: %w", name, err)
	}
	return nil
}

func installClaudeAgentSkill(name string, canonical string, destination string, refresh bool) error {
	if refresh {
		if err := os.RemoveAll(destination); err != nil {
			return fmt.Errorf("refresh Claude skill %s: %w", name, err)
		}
	} else if exists, usable, err := localSkillPathState(destination); err != nil {
		return err
	} else if exists && usable {
		return nil
	} else if exists {
		return fmt.Errorf("Claude skill path %s exists but does not contain SKILL.md; use --refresh-skills to replace it", destination)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create Claude skill parent: %w", err)
	}
	relative, err := filepath.Rel(filepath.Dir(destination), canonical)
	if err != nil {
		relative = canonical
	}
	if err := createAgentSkillSymlink(relative, destination); err == nil {
		if _, statErr := os.Stat(filepath.Join(destination, "SKILL.md")); statErr == nil {
			return nil
		}
	}
	_ = os.RemoveAll(destination)
	if err := copyEmbeddedSkillDir(name, destination); err != nil {
		return fmt.Errorf("install Claude skill fallback %s: %w", name, err)
	}
	return nil
}

func localSkillPathState(path string) (bool, bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("inspect local skill path %s: %w", path, err)
	}
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		if os.IsNotExist(err) {
			return true, false, nil
		}
		return true, false, fmt.Errorf("inspect local skill file %s: %w", path, err)
	}
	return true, true, nil
}

func copyEmbeddedSkillDir(name string, destination string) error {
	source := path.Join("skills", name)
	return fs.WalkDir(skillbundle.AgentSkills, source, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := "."
		if current != source {
			rel = strings.TrimPrefix(current, source+"/")
		}
		target := destination
		if rel != "." {
			target = filepath.Join(destination, filepath.FromSlash(rel))
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := skillbundle.AgentSkills.ReadFile(current)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
