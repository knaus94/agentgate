package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesignPackageBytesInvalidateReviewedContract(t *testing.T) {
	f := newFixture(t)
	layoutFixture(t)
	plan := f.plan("-m implement the page")
	plan.Design = []DesignRef{screenFixture(t)}
	f.launch(plan)
	f.review()
	design := Current().Plan.Design[0]
	original := design.digest()
	write(t, filepath.Join(design.Package, "data", "spec.json"), `{"modified":true}`)
	if design.digest() == original {
		t.Fatal("changed spec reused the manifest's self-reported digest")
	}
	if err := verifyDesignPackage(design.Package); err == nil || !strings.Contains(err.Error(), "import it again") {
		t.Fatal(err)
	}
	f.fails("finish", "task contract changed", func() (any, error) { return Finish(f.cfg, Current(), FinishInput{Summary: "stale design"}) })
}

func TestDesignPackageCannotDropInventoryOrEscapeDirectory(t *testing.T) {
	newFixture(t)
	design := screenFixture(t)
	dir := filepath.Join(design.Screen, "package")
	design.Package = dir
	original := design.digest()
	file := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(file)
	must(t, err)
	var manifest map[string]any
	must(t, json.Unmarshal(raw, &manifest))
	delete(manifest, "files")
	changed, err := json.Marshal(manifest)
	must(t, err)
	write(t, file, string(changed))
	if err := verifyDesignPackage(dir); err == nil {
		t.Fatal("missing inventory accepted")
	}
	if design.digest() == original {
		t.Fatal("removing inventory retained prior evidence")
	}
	write(t, file, string(raw))
	if err := verifyDesignPackage(dir); err != nil {
		t.Fatal(err)
	}
	var inventory packageManifest
	must(t, json.Unmarshal(raw, &inventory))
	inventory.Files["../outside"] = strings.Repeat("0", 64)
	inventory.Digest = packageContentDigest(inventory.Files)
	changed, err = json.Marshal(inventory)
	must(t, err)
	write(t, file, string(changed))
	if err := verifyDesignPackage(dir); err == nil {
		t.Fatal("outside inventory path accepted")
	}
	write(t, file, string(raw))
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, outside, `{}`)
	must(t, os.Remove(filepath.Join(dir, "data", "spec.json")))
	must(t, os.Symlink(outside, filepath.Join(dir, "data", "spec.json")))
	if err := verifyDesignPackage(dir); err == nil {
		t.Fatal("package symlink accepted")
	}
}
