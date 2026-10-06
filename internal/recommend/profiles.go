package recommend

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// stopwords are ignored in when_to_use and avoid_when phrases.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "you": true, "your": true, "are": true,
	"use": true, "when": true, "this": true, "that": true, "from": true, "into": true, "work": true,
	"working": true, "any": true, "all": true, "not": true, "has": true, "have": true, "can": true,
	"code": true, "tasks": true, "task": true, "project": true, "projects": true, "repo": true,
	"repos": true, "repository": true, "file": true, "files": true, "daily": true, "need": true,
	"needs": true, "doing": true, "making": true, "new": true, "existing": true, "using": true,
	"about": true, "also": true, "other": true, "than": true, "only": true, "its": true, "etc": true,
}

// extWords maps a file extension to words it implies.
var extWords = map[string][]string{
	".tf": {"terraform", "infrastructure"}, ".tfvars": {"terraform", "infrastructure"}, ".hcl": {"terraform"},
	".go": {"go", "golang"}, ".py": {"python"}, ".ts": {"typescript"}, ".tsx": {"typescript", "react", "frontend"},
	".js": {"javascript"}, ".jsx": {"javascript", "react", "frontend"}, ".vue": {"vue", "frontend"},
	".rs": {"rust"}, ".java": {"java"}, ".kt": {"kotlin"}, ".rb": {"ruby"}, ".php": {"php"},
	".swift": {"swift"}, ".cs": {"csharp", "dotnet"}, ".sql": {"sql", "database"}, ".sh": {"shell", "bash"},
	".css": {"css", "frontend"}, ".scss": {"css", "frontend"}, ".html": {"html", "frontend"},
	".proto": {"protobuf", "grpc"}, ".ipynb": {"notebook", "data"}, ".bicep": {"azure", "infrastructure"},
}

// nameWords maps lower-cased file names to words they imply.
var nameWords = map[string][]string{
	"dockerfile": {"docker", "container"}, "docker-compose.yml": {"docker", "container"},
	"docker-compose.yaml": {"docker", "container"}, "compose.yaml": {"docker", "container"},
	"makefile": {"make"}, "chart.yaml": {"helm", "kubernetes"}, "kustomization.yaml": {"kubernetes"},
	"go.mod": {"go", "golang"}, "package.json": {"node", "javascript"}, "tsconfig.json": {"typescript"},
	"pyproject.toml": {"python"}, "requirements.txt": {"python"}, "cargo.toml": {"rust", "cargo"},
	"gemfile": {"ruby"}, "pom.xml": {"java", "maven"}, "build.gradle": {"java", "gradle"},
	"playbook.yml": {"ansible"}, "pulumi.yaml": {"pulumi", "infrastructure"},
}

// cliWords maps a CLI name to extra words.
var cliWords = map[string][]string{
	"terraform": {"infrastructure"}, "terragrunt": {"infrastructure", "terraform"},
	"docker": {"container", "containers"}, "kubectl": {"kubernetes"}, "helm": {"kubernetes"},
	"npm": {"node", "javascript"}, "pip": {"python"}, "cargo": {"rust"}, "mvn": {"java", "maven"},
}

// depWords are dependency names searched (as quoted strings or words) in
// manifests.
var depWords = []string{
	"react", "vue", "angular", "svelte", "next", "express", "jest", "vitest", "tailwindcss", "typescript",
	"django", "flask", "fastapi", "pytest", "pandas", "rails", "spring", "gin", "cobra",
}

// vocabulary maps a word found in the directory to the first reason it was
// found.
func vocabulary(sig *Signals) map[string]string {
	v := map[string]string{}
	add := func(word, reason string) {
		word = stem(strings.ToLower(word))
		if word == "" || len(word) < 2 {
			return
		}
		if _, ok := v[word]; !ok {
			v[word] = reason
		}
	}
	addTokens := func(s, reason string) {
		for _, t := range tokens(s) {
			add(t, reason)
		}
	}
	// Directory names: the last three segments of the working directory.
	segs := strings.Split(filepath.ToSlash(sig.Cwd), "/")
	if len(segs) > 3 {
		segs = segs[len(segs)-3:]
	}
	for _, s := range segs {
		addTokens(s, fmt.Sprintf("directory name %q", s))
	}
	for _, f := range sig.Files {
		base := strings.ToLower(path.Base(f))
		for _, w := range nameWords[base] {
			add(w, fmt.Sprintf("file %s", f))
		}
		ext := path.Ext(base)
		for _, w := range extWords[ext] {
			add(w, fmt.Sprintf("%s files (%s)", ext, f))
		}
		if ext != "" {
			add(strings.TrimPrefix(ext, "."), fmt.Sprintf("%s files (%s)", ext, f))
		}
	}
	for _, c := range sig.CLIs {
		add(c, fmt.Sprintf("the %s command is implied by the files", c))
		for _, w := range cliWords[c] {
			add(w, fmt.Sprintf("the %s command is implied by the files", c))
		}
	}
	names := make([]string, 0, len(sig.ManifestFiles))
	for n := range sig.ManifestFiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data := bytes.ToLower(sig.ManifestFiles[n])
		for _, d := range depWords {
			if bytes.Contains(data, []byte(`"`+d+`"`)) || bytes.Contains(data, []byte(d+"==")) ||
				bytes.Contains(data, []byte("\n"+d+"\n")) || bytes.Contains(data, []byte("/"+d+" ")) {
				add(d, fmt.Sprintf("dependency %s in %s", d, n))
			}
		}
	}
	for _, h := range sig.Hosts {
		addTokens(h, fmt.Sprintf("host %s", h))
	}
	return v
}

// tokens splits text into lower-case words of at least three letters, minus
// stopwords.
func tokens(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, w := range f {
		if len([]rune(w)) < 3 || stopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// stem folds a trailing plural "s" so "containers" matches "container".
func stem(w string) string {
	if len(w) > 4 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

func profileRecs(sig *Signals, profiles []ProfileInfo) []Recommendation {
	if len(profiles) == 0 {
		return nil
	}
	vocab := vocabulary(sig)
	byName := map[string]ProfileInfo{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	byID := map[string]Recommendation{}
	for _, p := range profiles {
		score, why := scoreProfile(p, vocab)
		if score < minProfileScore {
			continue
		}
		rec := Recommendation{Kind: KindProfile, Name: p.Name, Score: score, Why: why, Status: p.Status}
		if p.Status == "deprecated" {
			r, ok := byName[p.SupersededBy]
			if !ok || r.Status == "deprecated" || r.Name == p.Name {
				continue
			}
			rec = Recommendation{
				Kind: KindProfile, Name: r.Name, Score: score, Status: r.Status, Replaces: p.Name,
				Why: append([]string{fmt.Sprintf("replaces deprecated profile %s, which matched this directory", p.Name)}, why...),
			}
		}
		if cur, ok := byID[rec.Name]; ok && cur.Score >= rec.Score {
			continue
		}
		byID[rec.Name] = rec
	}
	out := make([]Recommendation, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	return out
}

// scoreProfile adds keywordWeight for every distinct when_to_use keyword found
// in the directory's vocabulary and subtracts avoidWeight for every distinct
// avoid_when keyword found.
func scoreProfile(p ProfileInfo, vocab map[string]string) (float64, []string) {
	var score float64
	var why []string
	seen := map[string]bool{}
	for _, phrase := range p.WhenToUse {
		for _, t := range tokens(phrase) {
			t = stem(t)
			reason, ok := vocab[t]
			if !ok || seen[t] {
				continue
			}
			seen[t] = true
			score += keywordWeight
			why = append(why, fmt.Sprintf("when_to_use %q mentions %q: %s", phrase, t, reason))
		}
	}
	if score == 0 {
		return 0, nil
	}
	avoided := map[string]bool{}
	for _, phrase := range p.AvoidWhen {
		for _, t := range tokens(phrase) {
			t = stem(t)
			reason, ok := vocab[t]
			if !ok || avoided[t] {
				continue
			}
			avoided[t] = true
			score -= avoidWeight
			why = append(why, fmt.Sprintf("avoid_when %q mentions %q: %s (lowers the score)", phrase, t, reason))
		}
	}
	return score, why
}
