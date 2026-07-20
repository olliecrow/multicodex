package multicodex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var guidanceNames = []string{"AGENTS.md", "AGENTS.override.md"}

// ProfileResources controls the optional resources multicodex manages in each profile.
type ProfileResources struct {
	Guidance *GuidanceResources `json:"guidance,omitempty"`
	Skills   *SkillResources    `json:"skills,omitempty"`
}

type GuidanceResources struct {
	Inherit *bool  `json:"inherit"`
	Source  string `json:"source,omitempty"`
}

type SkillResources struct {
	Inherit *bool     `json:"inherit"`
	Sources *[]string `json:"sources,omitempty"`
}

type ResourceChange struct {
	Action    string
	Path      string
	OldTarget string
	NewTarget string
}

func (c ResourceChange) String() string {
	parts := []string{fmt.Sprintf("%s %s", c.Action, c.Path)}
	if c.OldTarget != "" {
		parts = append(parts, "old target: "+c.OldTarget)
	}
	if c.NewTarget != "" {
		parts = append(parts, "new target: "+c.NewTarget)
	}
	return strings.Join(parts, "; ")
}

func printResourceChanges(changes []ResourceChange) {
	printResourceChangesTo(os.Stdout, changes)
}

func printResourceChangesToStderr(changes []ResourceChange) {
	printResourceChangesTo(os.Stderr, changes)
}

func printResourceChangesTo(writer io.Writer, changes []ResourceChange) {
	for _, change := range changes {
		fmt.Fprintln(writer, "profile resource:", change.String())
	}
}

func (r *ProfileResources) UnmarshalJSON(data []byte) error {
	type raw ProfileResources
	var decoded raw
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("profile_resources: %w", err)
	}
	*r = ProfileResources(decoded)
	return nil
}

func (r *GuidanceResources) UnmarshalJSON(data []byte) error {
	type raw GuidanceResources
	var decoded raw
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("guidance: %w", err)
	}
	if decoded.Inherit == nil {
		return errors.New("guidance: required field inherit is missing")
	}
	if !*decoded.Inherit && strings.TrimSpace(decoded.Source) != "" {
		return errors.New("guidance: source cannot be set when inherit is false")
	}
	*r = GuidanceResources(decoded)
	return nil
}

func (r *SkillResources) UnmarshalJSON(data []byte) error {
	type raw SkillResources
	var decoded raw
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if decoded.Inherit == nil {
		return errors.New("skills: required field inherit is missing")
	}
	if !*decoded.Inherit && decoded.Sources != nil && len(*decoded.Sources) > 0 {
		return errors.New("skills: sources cannot be set when inherit is false")
	}
	if *decoded.Inherit && decoded.Sources != nil && len(*decoded.Sources) == 0 {
		return errors.New("skills: explicit sources must not be empty when inherit is true")
	}
	*r = SkillResources(decoded)
	return nil
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

type resolvedProfileResources struct {
	guidance *resolvedGuidanceResources
	skills   *resolvedSkillResources
}

type resolvedGuidanceResources struct {
	inherit bool
	source  string
	desired map[string]string
}

type resolvedSkillResources struct {
	inherit bool
	sources []string
	desired map[string]string
}

// ResolveProfileResources validates configured resource paths without changing profile state.
func (s *Store) ResolveProfileResources(resources *ProfileResources) (*resolvedProfileResources, error) {
	if resources == nil {
		return nil, nil
	}
	resolved := &resolvedProfileResources{}
	if resources.Guidance != nil {
		guidance, err := s.resolveGuidanceResources(resources.Guidance)
		if err != nil {
			return nil, err
		}
		resolved.guidance = guidance
	}
	if resources.Skills != nil {
		skills, err := s.resolveSkillResources(resources.Skills)
		if err != nil {
			return nil, err
		}
		resolved.skills = skills
	}
	return resolved, nil
}

func (s *Store) resolveGuidanceResources(settings *GuidanceResources) (*resolvedGuidanceResources, error) {
	if settings.Inherit == nil {
		return nil, errors.New("profile_resources.guidance.inherit is required")
	}
	resolved := &resolvedGuidanceResources{inherit: *settings.Inherit, desired: map[string]string{}}
	if !resolved.inherit {
		if strings.TrimSpace(settings.Source) != "" {
			return nil, errors.New("profile_resources.guidance.source cannot be set when inherit is false")
		}
		return resolved, nil
	}
	source := strings.TrimSpace(settings.Source)
	if source == "" {
		resolved.source = s.paths.DefaultCodexHome
	} else {
		path, err := s.resolveResourcePath(source)
		if err != nil {
			return nil, fmt.Errorf("resolve profile_resources.guidance.source: %w", err)
		}
		resolved.source = path
	}
	if err := requireDirectory(resolved.source, "guidance source"); err != nil {
		return nil, err
	}
	for _, name := range guidanceNames {
		path := filepath.Join(resolved.source, name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect guidance source file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("guidance source is not a regular file: %s", path)
		}
		resolved.desired[name] = path
	}
	return resolved, nil
}

func (s *Store) resolveSkillResources(settings *SkillResources) (*resolvedSkillResources, error) {
	if settings.Inherit == nil {
		return nil, errors.New("profile_resources.skills.inherit is required")
	}
	resolved := &resolvedSkillResources{inherit: *settings.Inherit, desired: map[string]string{}}
	if !resolved.inherit {
		if settings.Sources != nil && len(*settings.Sources) > 0 {
			return nil, errors.New("profile_resources.skills.sources cannot be set when inherit is false")
		}
		return resolved, nil
	}
	if settings.Sources == nil {
		resolved.sources = []string{filepath.Join(s.paths.DefaultCodexHome, "skills")}
	} else {
		if len(*settings.Sources) == 0 {
			return nil, errors.New("profile_resources.skills.sources must not be empty")
		}
		seen := map[string]bool{}
		for i, source := range *settings.Sources {
			path, err := s.resolveResourcePath(source)
			if err != nil {
				return nil, fmt.Errorf("resolve profile_resources.skills.sources[%d]: %w", i, err)
			}
			canonical := canonicalProfilePath(path)
			if seen[canonical] {
				return nil, fmt.Errorf("profile_resources.skills.sources[%d] duplicates an earlier source: %s", i, source)
			}
			seen[canonical] = true
			resolved.sources = append(resolved.sources, path)
		}
	}
	for i, source := range resolved.sources {
		entries, err := os.ReadDir(source)
		if errors.Is(err, os.ErrNotExist) && settings.Sources == nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read skill source %s: %w", source, err)
		}
		for _, entry := range entries {
			name := strings.TrimSpace(entry.Name())
			if name == "" || name == "." || name == ".." {
				continue
			}
			if _, exists := resolved.desired[name]; !exists {
				resolved.desired[name] = filepath.Join(resolved.sources[i], name)
			}
		}
	}
	return resolved, nil
}

// validateProfileResourceDestinations checks profile-owned positions before any
// profile setup or resource reconciliation changes the filesystem.
func (s *Store) validateProfileResourceDestinations(codexHome string, policy *ProfileResources) error {
	if policy == nil {
		return nil
	}
	if policy.Guidance != nil {
		if err := validateOwnedLinkPositions(codexHome, guidanceNames, "profile guidance"); err != nil {
			return err
		}
	}
	defaultSkillsPath := filepath.Join(s.paths.DefaultCodexHome, "skills")
	if policy.Skills == nil {
		if _, err := os.ReadDir(defaultSkillsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read default skills dir: %w", err)
		}
	}

	profileSkillsPath := filepath.Join(codexHome, "skills")
	if err := ensurePathNotSymlinkIfExists(profileSkillsPath); err != nil {
		return err
	}
	info, err := os.Lstat(profileSkillsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect profile skills path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("profile skills path is not a directory: %s", profileSkillsPath)
	}
	if err := ensurePathPrefixesBelowRootNotSymlinks(codexHome, profileSkillsPath); err != nil {
		return err
	}
	entries, err := os.ReadDir(profileSkillsPath)
	if err != nil {
		return fmt.Errorf("read profile skills dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if err := validateOwnedLinkPositions(profileSkillsPath, names, "profile skill"); err != nil {
		return err
	}
	if policy.Skills != nil {
		return nil
	}
	for _, name := range names {
		path := filepath.Join(profileSkillsPath, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := resolveExistingSymlinkTarget(path)
		if errors.Is(err, os.ErrNotExist) {
			target, err = resolveBrokenManagedSymlinkTarget(path)
		}
		if err != nil {
			return fmt.Errorf("resolve profile skill symlink %s: %w", path, err)
		}
		if !pathIsInsideRoot(defaultSkillsPath, target) {
			return fmt.Errorf("profile skill symlink must point under default skills directory: %s", path)
		}
	}
	return nil
}

func validateOwnedLinkPositions(root string, names []string, label string) error {
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s %s: %w", label, path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if _, err := os.Readlink(path); err != nil {
			return fmt.Errorf("read %s symlink %s: %w", label, path, err)
		}
	}
	return nil
}

func (s *Store) resolveResourcePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is blank")
	}
	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if value == "~" {
			value = home
		} else if strings.HasPrefix(value, "~/") {
			value = filepath.Join(home, value[2:])
		} else {
			return "", fmt.Errorf("unsupported home path %q; use ~ or ~/path", value)
		}
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(filepath.Dir(s.paths.ConfigPath), value)
	}
	return filepath.Clean(value), nil
}

func requireDirectory(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s", label, path)
	}
	return nil
}

func (s *Store) reconcileProfileResources(codexHome string, policy *ProfileResources, resolved *resolvedProfileResources) ([]ResourceChange, error) {
	if policy == nil {
		return nil, s.ensureProfileSkills(codexHome)
	}
	var changes []ResourceChange
	if resolved.guidance != nil {
		guidanceChanges, err := reconcileGuidance(codexHome, resolved.guidance)
		if err != nil {
			return nil, err
		}
		changes = append(changes, guidanceChanges...)
	}
	if resolved.skills == nil {
		if err := s.ensureProfileSkills(codexHome); err != nil {
			return nil, err
		}
	} else {
		skillChanges, err := reconcileExplicitSkills(codexHome, resolved.skills)
		if err != nil {
			return nil, err
		}
		changes = append(changes, skillChanges...)
	}
	return changes, nil
}

func reconcileGuidance(codexHome string, resolved *resolvedGuidanceResources) ([]ResourceChange, error) {
	localOverride := false
	for _, name := range guidanceNames {
		info, err := os.Lstat(filepath.Join(codexHome, name))
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			localOverride = true
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect profile guidance %s: %w", name, err)
		}
	}
	desired := resolved.desired
	if !resolved.inherit || localOverride {
		desired = map[string]string{}
	}
	return reconcileOwnedLinks(codexHome, guidanceNames, desired, "profile guidance")
}

func reconcileExplicitSkills(codexHome string, resolved *resolvedSkillResources) ([]ResourceChange, error) {
	profileSkillsPath := filepath.Join(codexHome, "skills")
	if err := ensurePathNotSymlinkIfExists(profileSkillsPath); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(profileSkillsPath); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("profile skills path is not a directory: %s", profileSkillsPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect profile skills path: %w", err)
	}
	if err := ensurePathPrefixesBelowRootNotSymlinks(codexHome, profileSkillsPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profileSkillsPath, 0o700); err != nil {
		return nil, fmt.Errorf("create profile skills dir: %w", err)
	}
	if err := os.Chmod(profileSkillsPath, 0o700); err != nil {
		return nil, fmt.Errorf("secure profile skills dir permissions: %w", err)
	}
	entries, err := os.ReadDir(profileSkillsPath)
	if err != nil {
		return nil, fmt.Errorf("read profile skills dir: %w", err)
	}
	names := make([]string, 0, len(entries)+len(resolved.desired))
	seen := map[string]bool{}
	for _, entry := range entries {
		names = append(names, entry.Name())
		seen[entry.Name()] = true
	}
	for name := range resolved.desired {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	desired := resolved.desired
	if !resolved.inherit {
		desired = map[string]string{}
	}
	return reconcileOwnedLinks(profileSkillsPath, names, desired, "profile skill")
}

func reconcileOwnedLinks(root string, names []string, desired map[string]string, label string) ([]ResourceChange, error) {
	var changes []ResourceChange
	for _, name := range names {
		path := filepath.Join(root, name)
		want, wanted := desired[name]
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			oldTarget, err := os.Readlink(path)
			if err != nil {
				return nil, fmt.Errorf("read %s symlink %s: %w", label, path, err)
			}
			if wanted {
				resolvedOld := resolveLinkTarget(path, oldTarget)
				if canonicalProfilePath(resolvedOld) == canonicalProfilePath(want) {
					continue
				}
			}
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("remove %s symlink %s: %w", label, path, err)
			}
			action := "removed"
			if wanted {
				action = "retargeted"
			}
			change := ResourceChange{Action: action, Path: path, OldTarget: oldTarget}
			if wanted {
				if err := os.Symlink(want, path); err != nil {
					return nil, fmt.Errorf("link %s %s: %w", label, path, err)
				}
				change.NewTarget = want
			}
			changes = append(changes, change)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect %s %s: %w", label, path, err)
		}
		if !wanted {
			continue
		}
		if err := os.Symlink(want, path); err != nil {
			return nil, fmt.Errorf("link %s %s: %w", label, path, err)
		}
		changes = append(changes, ResourceChange{Action: "linked", Path: path, NewTarget: want})
	}
	return changes, nil
}

func resolveLinkTarget(linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}
