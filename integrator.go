package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IntegrateParts moves extracted assets and returns tracking info for Undo functionality
func IntegrateParts(assets *KiCadAssets, category string, targetRepoRoot string, repoName string) ([]string, string, string, error) {

	prettyFolder := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
	shapesFolder := filepath.Join(targetRepoRoot, "packages3d", fmt.Sprintf("%s.3dshapes", category))
	symbolsFolder := filepath.Join(targetRepoRoot, "symbols")
	blocksFolder := filepath.Join(targetRepoRoot, "blocks", category)

	for _, dir := range []string{prettyFolder, shapesFolder, symbolsFolder, blocksFolder} {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return nil, "", "", fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	var finalModelName string
	var addedFiles []string
	var masterSym string
	var backupSym string

	// 1. Handle 3D Models
	if assets.ModelPath != "" {
		finalModelName = filepath.Base(assets.ModelPath)
		destModelPath := filepath.Join(shapesFolder, finalModelName)
		if err := copyFile(assets.ModelPath, destModelPath); err != nil {
			fmt.Println("Warning: failed to copy 3D model:", err)
		} else {
			addedFiles = append(addedFiles, destModelPath)
			fmt.Println("--> Copied 3D Model to:", destModelPath)
		}
	}

	// 2. Handle Footprints
	var finalFootprintName string
	if assets.FootprintPath != "" {
		finalFootprintName = strings.TrimSuffix(filepath.Base(assets.FootprintPath), ".kicad_mod")
		destFootprintPath := filepath.Join(prettyFolder, filepath.Base(assets.FootprintPath))

		var fpErr error
		if finalModelName != "" {
			fpErr = patchFootprint3DPath(assets.FootprintPath, destFootprintPath, category, finalModelName, repoName)
			fmt.Println("--> Copied & Patched Footprint to:", destFootprintPath)
		} else {
			fpErr = copyFile(assets.FootprintPath, destFootprintPath)
			fmt.Println("--> Copied Footprint to:", destFootprintPath)
		}
		if fpErr != nil {
			fmt.Println("Warning: failed to write footprint:", fpErr)
		} else {
			addedFiles = append(addedFiles, destFootprintPath)
			UpdateKiCadFpTable(category, prettyFolder)
		}
	}

	// 3. Handle Symbols
	if assets.SymbolPath != "" {
		masterSym = filepath.Join(symbolsFolder, fmt.Sprintf("%s.kicad_sym", category))
		backupSym = masterSym + ".bak"

		masterExisted := false
		if _, err := os.Stat(masterSym); err == nil {
			masterExisted = true
			if err := copyFile(masterSym, backupSym); err != nil {
				return addedFiles, "", "", fmt.Errorf("failed to back up symbol library: %w", err)
			}
		}

		if err := injectSymbol(assets.SymbolPath, masterSym, category, finalFootprintName); err != nil {
			// Roll back the backup so we don't leave a stale .bak file
			if masterExisted {
				os.Rename(backupSym, masterSym)
			} else {
				os.Remove(masterSym)
			}
			return addedFiles, "", "", fmt.Errorf("failed to inject symbol: %w", err)
		}
		fmt.Println("--> Injected & Sanitized Symbol into:", masterSym)
		UpdateKiCadSymTable(category, masterSym)

		if !masterExisted {
			// Master was newly created — track it in addedFiles so UndoAction can
			// delete it cleanly. Clear the backup/master return values so UndoAction
			// doesn't attempt a backup-restore (there is no backup).
			addedFiles = append(addedFiles, masterSym)
			masterSym = ""
			backupSym = ""
		}
	}

	// 4. Handle Design Blocks
	if assets.SchBlockPath != "" {
		destSch := filepath.Join(blocksFolder, filepath.Base(assets.SchBlockPath))
		if err := copyFile(assets.SchBlockPath, destSch); err != nil {
			fmt.Println("Warning: failed to copy schematic block:", err)
		} else {
			addedFiles = append(addedFiles, destSch)
			fmt.Println("--> Copied Schematic Design Block to:", destSch)
		}
	}
	if assets.PcbBlockPath != "" {
		destPcb := filepath.Join(blocksFolder, filepath.Base(assets.PcbBlockPath))
		if err := copyFile(assets.PcbBlockPath, destPcb); err != nil {
			fmt.Println("Warning: failed to copy PCB block:", err)
		} else {
			addedFiles = append(addedFiles, destPcb)
			fmt.Println("--> Copied PCB Design Block to:", destPcb)
		}
	}

	return addedFiles, masterSym, backupSym, nil
}

// InitializeKiCadLibraries pre-registers all default categories in KiCad's global tables.
// This ensures they appear in the KiCad UI immediately on first launch.
func InitializeKiCadLibraries(conf Config) {
	if conf.BaseLibPath == "" || len(conf.Repositories) == 0 {
		return
	}

	fmt.Println("--> Performing first-launch KiCad library registration...")

	UpdateKiCadEnvVar(conf.BaseLibPath)

	defaultRepo := conf.Repositories[0].Name
	targetRepoRoot := filepath.Join(conf.BaseLibPath, defaultRepo)

	for _, category := range conf.Categories {
		// Setup Footprint Library Folder
		prettyPath := filepath.Join(targetRepoRoot, "footprints", fmt.Sprintf("%s.pretty", category))
		os.MkdirAll(prettyPath, os.ModePerm)
		UpdateKiCadFpTable(category, prettyPath)

		// Setup Symbol Library File
		symDir := filepath.Join(targetRepoRoot, "symbols")
		symPath := filepath.Join(symDir, fmt.Sprintf("%s.kicad_sym", category))

		if _, err := os.Stat(symPath); os.IsNotExist(err) {
			os.MkdirAll(symDir, os.ModePerm)
			emptyLib := "(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)\n)\n"
			os.WriteFile(symPath, []byte(emptyLib), 0644)
		}
		UpdateKiCadSymTable(category, symPath)
	}
}

func injectSymbol(sourceFile, masterFile, category, footprintName string) error {
	srcBytes, err := os.ReadFile(sourceFile)
	if err != nil {
		return err
	}
	srcContent := string(srcBytes)

	reSymbolBlock := regexp.MustCompile(`(?s)\(\s*symbol\s+".+`)
	match := reSymbolBlock.FindString(srcContent)
	if match == "" {
		return fmt.Errorf("could not find a valid (symbol ...) block in source file")
	}

	lastParenIdx := strings.LastIndex(match, ")")
	if lastParenIdx == -1 {
		return fmt.Errorf("malformed source symbol file")
	}
	extractedSymbol := strings.TrimSpace(match[:lastParenIdx])

	if footprintName != "" {
		reFootprintProp := regexp.MustCompile(`\(property\s+"Footprint"\s+"[^"]*"`)
		newProp := fmt.Sprintf(`(property "Footprint" "%s:%s"`, category, footprintName)
		extractedSymbol = reFootprintProp.ReplaceAllString(extractedSymbol, newProp)
	}

	var masterContent string
	if _, err := os.Stat(masterFile); os.IsNotExist(err) {
		masterContent = `(kicad_symbol_lib (version 20211014) (generator kicad_symbol_editor)
)`
	} else {
		masterBytes, err := os.ReadFile(masterFile)
		if err != nil {
			return err
		}
		masterContent = string(masterBytes)
	}

	masterLastParenIdx := strings.LastIndex(masterContent, ")")
	if masterLastParenIdx == -1 {
		return fmt.Errorf("master symbol file is malformed")
	}

	newMasterContent := masterContent[:masterLastParenIdx] + "\n  " + extractedSymbol + "\n)\n"
	return os.WriteFile(masterFile, []byte(newMasterContent), 0644)
}

func UpdateKiCadEnvVar(basePath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	versionRegex := regexp.MustCompile(`^\d+(\.\d+)?$`)

	for _, entry := range entries {
		if !entry.IsDir() || !versionRegex.MatchString(entry.Name()) {
			continue
		}

		commonJsonPath := filepath.Join(kicadBase, entry.Name(), "kicad_common.json")

		var configData map[string]interface{}
		fileBytes, err := os.ReadFile(commonJsonPath)
		if err == nil {
			json.Unmarshal(fileBytes, &configData)
		}

		if configData == nil {
			configData = make(map[string]interface{})
		}

		// 1. Safely handle the "environment" section
		env, ok := configData["environment"].(map[string]interface{})
		if !ok || env == nil {
			env = make(map[string]interface{})
			configData["environment"] = env
		}

		// 2. Safely handle the "vars" section
		vars, ok := env["vars"].(map[string]interface{})
		if !ok || vars == nil {
			vars = make(map[string]interface{})
			env["vars"] = vars
		}

		// 3. Update the variable
		vars["KICAD_USER_3DMODEL_DIR"] = basePath

		newJson, err := json.MarshalIndent(configData, "", "  ")
		if err != nil {
			continue
		}

		err = os.WriteFile(commonJsonPath, newJson, 0644)
		if err == nil {
			fmt.Printf("--> Registered KICAD_USER_3DMODEL_DIR in KiCad %s\n", entry.Name())
		}
	}
	return nil
}

func patchFootprint3DPath(src, dest, category, modelFileName, repoName string) error {
	contentBytes, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	// Define the new path string with the environment variable
	newModelPath := fmt.Sprintf("(model \"${KICAD_USER_3DMODEL_DIR}/%s/packages3d/%s.3dshapes/%s\"\n    (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0))\n  )", repoName, category, modelFileName)

	re := regexp.MustCompile(`(?i)\(model\s+"?([^"\)]+\.(?:step|stp|wrl))"?.*?\n?\s*\)`)

	var patchedContent string
	if re.MatchString(content) {
		// Scenario 1: Model exists, replace it
		patchedContent = re.ReplaceAllString(content, newModelPath)
	} else {
		// Scenario 2: No model tag, inject it before the final closing bracket
		lastParenIdx := strings.LastIndex(content, ")")
		if lastParenIdx == -1 {
			return fmt.Errorf("malformed footprint file")
		}
		patchedContent = content[:lastParenIdx] + "  " + newModelPath + "\n)"
	}

	return os.WriteFile(dest, []byte(patchedContent), 0644)
}

func copyFile(src, dest string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func UpdateKiCadSymTable(libNickname, libPath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	versionRegex := regexp.MustCompile(`^\d+(\.\d+)?$`)

	for _, entry := range entries {
		if !entry.IsDir() || !versionRegex.MatchString(entry.Name()) {
			continue
		}

		tablePath := filepath.Join(kicadBase, entry.Name(), "sym-lib-table")

		content, err := os.ReadFile(tablePath)
		if err != nil {
			content = []byte("(sym_lib_table\n)")
		}

		sContent := string(content)
		if strings.Contains(sContent, fmt.Sprintf("(name %q)", libNickname)) {
			continue
		}

		entryStr := fmt.Sprintf("  (lib (name %q)(type \"KiCad\")(uri %q)(options \"\")(descr \"Added by KiCadLibMgr\"))\n", libNickname, libPath)
		lastIdx := strings.LastIndex(sContent, ")")
		if lastIdx == -1 {
			continue
		}

		newContent := sContent[:lastIdx] + entryStr + ")\n"
		os.WriteFile(tablePath, []byte(newContent), 0644)
		fmt.Printf("--> Registered symbol library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}

func UpdateKiCadFpTable(libNickname, libPath string) error {
	kicadBase := filepath.Join(kicadConfigDir(), "kicad")

	entries, err := os.ReadDir(kicadBase)
	if err != nil {
		return err
	}

	versionRegex := regexp.MustCompile(`^\d+(\.\d+)?$`)

	for _, entry := range entries {
		if !entry.IsDir() || !versionRegex.MatchString(entry.Name()) {
			continue
		}

		tablePath := filepath.Join(kicadBase, entry.Name(), "fp-lib-table")

		content, err := os.ReadFile(tablePath)
		if err != nil {
			content = []byte("(fp_lib_table\n)")
		}

		sContent := string(content)
		if strings.Contains(sContent, fmt.Sprintf("(name %q)", libNickname)) {
			continue
		}

		entryStr := fmt.Sprintf("  (lib (name %q)(type \"KiCad\")(uri %q)(options \"\")(descr \"Added by KiCadLibMgr\"))\n", libNickname, libPath)
		lastIdx := strings.LastIndex(sContent, ")")
		if lastIdx == -1 {
			continue
		}

		newContent := sContent[:lastIdx] + entryStr + ")\n"
		os.WriteFile(tablePath, []byte(newContent), 0644)
		fmt.Printf("--> Registered footprint library %s in KiCad %s\n", libNickname, entry.Name())
	}
	return nil
}
