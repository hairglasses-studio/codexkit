package skillsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

var resourceNames = []string{"references", "scripts", "assets", "examples", "templates"}

type ResourceLink struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	SHA256   string `json:"sha256"`
	State    string `json:"state"`
	LinkText string `json:"link_text"`
}

type ResourceReport struct {
	Source            string         `json:"source"`
	Target            string         `json:"target"`
	SourceSkillSHA256 string         `json:"source_skill_sha256"`
	TargetSkillSHA256 string         `json:"target_skill_sha256"`
	Links             []ResourceLink `json:"links"`
	PlanSHA256        string         `json:"plan_sha256"`
	Applied           bool           `json:"applied"`
	Errors            []string       `json:"errors,omitempty"`
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// resourceDigest never follows a symlink outside its explicit resource tree.
func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("regular resource file required")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("resource byte limit exceeded")
	}
	return raw, nil
}

func resourceDigest(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	h := sha256.New()
	count, total := 0, int64(0)
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > 4096 {
			return fmt.Errorf("resource entry limit exceeded")
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			value, err := root.Readlink(rel)
			if err != nil {
				return err
			}
			if _, err := root.Stat(rel); err != nil {
				return fmt.Errorf("unreadable or escaping resource symlink: %s", rel)
			}
			fmt.Fprintf(h, "%s\x00", value)
		case info.IsDir():
		case info.Mode().IsRegular():
			raw, err := readBounded(root, rel, 8<<20)
			if err != nil {
				return err
			}
			total += int64(len(raw))
			if total > 32<<20 {
				return fmt.Errorf("resource byte limit exceeded")
			}
			h.Write(raw)
			h.Write([]byte{0})
		default:
			return fmt.Errorf("unsupported resource entry: %s", rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func planResources(source, target string) (ResourceReport, error) {
	r := ResourceReport{Source: source, Target: target, Links: []ResourceLink{}}
	for _, dir := range []string{source, target} {
		if !filepath.IsAbs(dir) {
			return r, fmt.Errorf("use absolute skill directories")
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return r, err
		}
		if real != filepath.Clean(dir) {
			return r, fmt.Errorf("skill directory must be canonical, without directory symlinks: %s", dir)
		}
	}
	for i, dir := range []string{source, target} {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return r, err
		}
		raw, err := readBounded(root, "SKILL.md", 1<<20)
		root.Close()
		if err != nil {
			return r, err
		}
		if i == 0 {
			r.SourceSkillSHA256 = hashBytes(raw)
		} else {
			r.TargetSkillSHA256 = hashBytes(raw)
		}
	}

	links, err := resourceLinks(source, target)
	if err != nil {
		return r, err
	}
	r.Links = links
	raw, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	r.PlanSHA256 = hashBytes(raw)
	return r, nil
}

func resourceLinks(source, target string) ([]ResourceLink, error) {
	r := ResourceReport{Links: []ResourceLink{}}
	for _, name := range resourceNames {
		src, dst := filepath.Join(source, name), filepath.Join(target, name)
		info, err := os.Lstat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("source resource must be a real directory: %s", src)
		}
		digest, err := resourceDigest(src)
		if err != nil {
			return nil, err
		}
		linkText, err := filepath.Rel(target, src)
		if err != nil {
			return nil, err
		}
		link := ResourceLink{Source: src, Target: dst, SHA256: digest, State: "missing", LinkText: linkText}
		existing, err := os.Lstat(dst)
		if err == nil {
			switch {
			case existing.Mode()&os.ModeSymlink != 0:
				resolved, e := filepath.EvalSymlinks(dst)
				if e != nil || resolved != src {
					return nil, fmt.Errorf("foreign resource link preserved: %s", dst)
				}
				link.State = "linked"
			case existing.IsDir():
				observed, e := resourceDigest(dst)
				if e != nil || observed != digest {
					return nil, fmt.Errorf("different existing resources preserved: %s", dst)
				}
				link.State = "identical"
			default:
				return nil, fmt.Errorf("existing resource preserved: %s", dst)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		r.Links = append(r.Links, link)
	}
	return r.Links, nil
}

func syncProjectedResources(report *SyncReport, mode syncMode, source, target string) {
	links, err := resourceLinks(source, target)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}
	for _, link := range links {
		action := "unchanged"
		if link.State == "missing" {
			report.PendingChanges = true
			action = "create"
			if mode == modeWrite {
				root, err := os.OpenRoot(target)
				if err != nil {
					report.Errors = append(report.Errors, err.Error())
					return
				}
				err = root.Symlink(link.LinkText, filepath.Base(link.Target))
				root.Close()
				if err != nil {
					report.Errors = append(report.Errors, err.Error())
					return
				}
			}
		}
		report.Actions = append(report.Actions, SyncAction{Action: action, SrcPath: link.Source, DstPath: link.Target, Message: "Skill support resources: " + link.Target})
	}
}

// SyncResources adds only missing support-directory links to an existing skill.
// It never rewrites instruction files, metadata, existing resources or config.
func SyncResources(source, target string, apply bool, expectedPlan string) ResourceReport {
	r, err := planResources(source, target)
	if err != nil {
		r.Errors = []string{err.Error()}
		return r
	}
	if !apply {
		return r
	}
	if expectedPlan == "" || r.PlanSHA256 != expectedPlan {
		r.Errors = []string{"reviewed resource plan changed or was not supplied"}
		return r
	}
	// OpenRoot pins the destination directory so replacing a parent path cannot
	// redirect link creation into another skill or the canonical source.
	root, err := os.OpenRoot(target)
	if err != nil {
		r.Errors = []string{err.Error()}
		return r
	}
	defer root.Close()
	fresh, err := planResources(source, target)
	if err != nil || fresh.PlanSHA256 != r.PlanSHA256 {
		r.Errors = []string{"resource source or target changed before apply"}
		return r
	}
	for i, link := range r.Links {
		if link.State != "missing" {
			continue
		}
		if err := root.Symlink(link.LinkText, filepath.Base(link.Target)); err != nil {
			r.Errors = append(r.Errors, err.Error())
			return r
		}
		r.Links[i].State = "created"
	}
	r.Applied = true
	return r
}
