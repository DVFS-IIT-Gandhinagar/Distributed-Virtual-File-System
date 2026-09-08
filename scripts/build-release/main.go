package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Target struct {
	OS      string
	Arch    string
	Archive string
}

func main() {
	loadEnv(".env", "../.env", "../../.env")

	clientOnly := flag.Bool("client-only", false, "Only build and package client binaries")
	nodesOnly := flag.Bool("nodes-only", false, "Only build and package node binaries")
	outDir := flag.String("out", "./release", "Output directory for release archives")
	version := flag.String("version", "", "Version tag (defaults to git describe or 'dev')")
	flag.Parse()

	if *version == "" {
		*version = resolveVersion()
	}

	log.Printf("=================================================================")
	log.Printf("            DVFS MULTI-PLATFORM RELEASE BUILDER                  ")
	log.Printf("=================================================================")
	log.Printf("Version:    %s", *version)
	log.Printf("Output Dir: %s", *outDir)

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	tempBuildDir, err := os.MkdirTemp("", "dvfs-release-*")
	if err != nil {
		log.Fatalf("Failed to create temporary build directory: %v", err)
	}
	defer os.RemoveAll(tempBuildDir)

	var archivesCreated []string

	flavors := []struct {
		Name   string
		Tags   string
		Suffix string
	}{
		{Name: "Google Auth (Default)", Tags: "use_google_auth", Suffix: ""},
		{Name: "No-Auth", Tags: "", Suffix: "-no-auth"},
	}

	for _, f := range flavors {
		log.Printf("\n=================================================================")
		log.Printf("           BUILDING FLAVOR: %s", f.Name)
		log.Printf("=================================================================")

		// 1. Build Clients
		if !*nodesOnly {
			log.Printf("\n--- Building Client Distributions (%s) ---", f.Name)
			clientTargets := []Target{
				{OS: "windows", Arch: "amd64", Archive: "dvfs-client-windows-amd64.zip"},
				{OS: "linux", Arch: "amd64", Archive: "dvfs-client-linux-amd64.tar.gz"},
				{OS: "linux", Arch: "arm64", Archive: "dvfs-client-linux-arm64.tar.gz"},
				{OS: "darwin", Arch: "arm64", Archive: "dvfs-client-darwin-arm64.tar.gz"},
				{OS: "darwin", Arch: "amd64", Archive: "dvfs-client-darwin-amd64.tar.gz"},
			}

			for _, t := range clientTargets {
				files, err := buildClientDistribution(t, tempBuildDir, *outDir, *version, f.Tags, f.Suffix)
				if err != nil {
					log.Fatalf("[FATAL] Failed to build client distribution for %s/%s (%s): %v", t.OS, t.Arch, f.Name, err)
				}
				archivesCreated = append(archivesCreated, files...)
			}
		}

		// 2. Build Cluster Nodes
		if !*clientOnly {
			log.Printf("\n--- Building Cluster Node Distributions (%s) ---", f.Name)
			nodeTargets := []Target{
				{OS: "linux", Arch: "arm64", Archive: "dvfs-nodes-linux-arm64.tar.gz"},
				{OS: "linux", Arch: "amd64", Archive: "dvfs-nodes-linux-amd64.tar.gz"},
			}

			for _, t := range nodeTargets {
				archivePath, err := buildNodeDistribution(t, tempBuildDir, *outDir, *version, f.Tags, f.Suffix)
				if err != nil {
					log.Fatalf("[FATAL] Failed to build node distribution for %s/%s (%s): %v", t.OS, t.Arch, f.Name, err)
				}
				archivesCreated = append(archivesCreated, archivePath)
			}
		}
	}

	// 3. Generate Checksums
	log.Println("\n--- Generating SHA256 Checksums ---")
	checksumFile := filepath.Join(*outDir, "SHA256SUMS.txt")
	if err := generateChecksums(archivesCreated, checksumFile); err != nil {
		log.Fatalf("Failed to generate checksums: %v", err)
	}

	log.Printf("\nRelease builds completed successfully! Artifacts in: %s", *outDir)
	for _, a := range archivesCreated {
		log.Printf("  - %s", filepath.Base(a))
	}
	log.Printf("  - SHA256SUMS.txt\n")
}

func buildClientDistribution(target Target, tempDir, outDir, version, tags, suffix string) ([]string, error) {
	ext := ""
	if target.OS == "windows" {
		ext = ".exe"
	}

	standaloneName := fmt.Sprintf("dvfs-client%s-%s-%s%s", suffix, target.OS, target.Arch, ext)
	standaloneBin := filepath.Join(outDir, standaloneName)
	log.Printf("Compiling standalone client executable: %s (tags: %q)", standaloneName, tags)

	if err := compileBinary("./cmd/client", standaloneBin, target.OS, target.Arch, version, tags); err != nil {
		return nil, err
	}

	created := []string{standaloneBin}

	stagingDir := filepath.Join(tempDir, fmt.Sprintf("client%s-%s-%s", suffix, target.OS, target.Arch))
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return created, nil
	}

	archiveBinName := "dvfs-client" + ext
	if err := copyFile(standaloneBin, filepath.Join(stagingDir, archiveBinName)); err != nil {
		return created, nil
	}

	archiveName := target.Archive
	if suffix != "" {
		if strings.HasPrefix(archiveName, "dvfs-client-") {
			archiveName = strings.Replace(archiveName, "dvfs-client-", "dvfs-client"+suffix+"-", 1)
		} else if strings.HasSuffix(archiveName, ".zip") {
			archiveName = strings.TrimSuffix(archiveName, ".zip") + suffix + ".zip"
		} else if strings.HasSuffix(archiveName, ".tar.gz") {
			archiveName = strings.TrimSuffix(archiveName, ".tar.gz") + suffix + ".tar.gz"
		}
	}

	finalArchive := filepath.Join(outDir, archiveName)
	var err error
	if strings.HasSuffix(archiveName, ".zip") {
		err = createZipArchive(stagingDir, finalArchive)
	} else {
		err = createTarGzArchive(stagingDir, finalArchive)
	}

	if err == nil {
		created = append(created, finalArchive)
		log.Printf("Packaged %s", archiveName)
	}

	return created, nil
}

func buildNodeDistribution(target Target, tempDir, outDir, version, tags, suffix string) (string, error) {
	stagingDir := filepath.Join(tempDir, fmt.Sprintf("nodes%s-%s-%s", suffix, target.OS, target.Arch))
	binDir := filepath.Join(stagingDir, "bin")
	scriptsDir := filepath.Join(stagingDir, "scripts")

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return "", err
	}

	// 1. Compile Metaserver
	log.Printf("Compiling metaserver for %s/%s (tags: %q)", target.OS, target.Arch, tags)
	if err := compileBinary("./cmd/metaserver", filepath.Join(binDir, "metaserver"), target.OS, target.Arch, version, tags); err != nil {
		return "", err
	}

	// 2. Compile Fileserver
	log.Printf("Compiling fileserver for %s/%s (tags: %q)", target.OS, target.Arch, tags)
	if err := compileBinary("./cmd/fileserver", filepath.Join(binDir, "fileserver"), target.OS, target.Arch, version, tags); err != nil {
		return "", err
	}

	// 3. Compile Admin Server
	log.Printf("Compiling admin server for %s/%s (tags: %q)", target.OS, target.Arch, tags)
	if err := compileBinary("./cmd/admin", filepath.Join(binDir, "admin"), target.OS, target.Arch, version, tags); err != nil {
		return "", err
	}

	// 4. Bundle scripts and systemd templates
	filesToBundle := []string{
		"scripts/start-metaserver.sh",
		"scripts/start-fileserver.sh",
		"scripts/start-admin.sh",
		"scripts/dvfs-metaserver.service",
		"scripts/dvfs-fileserver.service",
		"scripts/dvfs-admin.service",
		"scripts/dvfs-fileserver@.service",
	}

	for _, f := range filesToBundle {
		if _, err := os.Stat(f); err == nil {
			dest := filepath.Join(scriptsDir, filepath.Base(f))
			_ = copyFile(f, dest)
		}
	}

	archiveName := target.Archive
	if suffix != "" {
		if strings.HasPrefix(archiveName, "dvfs-nodes-") {
			archiveName = strings.Replace(archiveName, "dvfs-nodes-", "dvfs-nodes"+suffix+"-", 1)
		} else {
			archiveName = strings.TrimSuffix(archiveName, ".tar.gz") + suffix + ".tar.gz"
		}
	}

	finalArchive := filepath.Join(outDir, archiveName)
	if err := createTarGzArchive(stagingDir, finalArchive); err != nil {
		return "", err
	}

	log.Printf("Packaged %s", archiveName)
	return finalArchive, nil
}

func compileBinary(srcPkg, outBin, targetOS, targetArch, version, tags string) error {
	ldflags := fmt.Sprintf("-s -w -X main.Version=%s", version)
	if clientID := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID")); clientID != "" {
		ldflags += fmt.Sprintf(" -X github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth.DefaultClientID=%s", clientID)
	}
	if clientSecret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET")); clientSecret != "" {
		ldflags += fmt.Sprintf(" -X github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/internal/auth.DefaultClientSecret=%s", clientSecret)
	}
	args := []string{"build", "-trimpath"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "-ldflags", ldflags, "-o", outBin, srcPkg)

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func loadEnv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				if os.Getenv(key) == "" {
					_ = os.Setenv(key, val)
				}
			}
		}
		_ = f.Close()
	}
}

func resolveVersion() string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		return strings.TrimSpace(string(out))
	}
	return "dev"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func createZipArchive(srcDir, zipPath string) error {
	outFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	w := zip.NewWriter(outFile)
	defer w.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		relPath = filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

func createTarGzArchive(srcDir, tarGzPath string) error {
	outFile, err := os.Create(tarGzPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	gz := gzip.NewWriter(outFile)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Mode = 0755

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
}

func generateChecksums(files []string, outPath string) error {
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	for _, f := range files {
		h := sha256.New()
		file, err := os.Open(f)
		if err != nil {
			return err
		}
		if _, err := io.Copy(h, file); err != nil {
			file.Close()
			return err
		}
		file.Close()

		sum := hex.EncodeToString(h.Sum(nil))
		fmt.Fprintf(outFile, "%s  %s\n", sum, filepath.Base(f))
	}
	return nil
}
