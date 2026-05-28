package wsfold

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

const (
	workspaceTemplate         = "{\n  \"folders\": [],\n  \"settings\": {}\n}\n"
	topLevelMemberIndent      = "\n  "
	arrayElementIndent        = "\n    "
	arrayClosingIndent        = "\n  "
	nestedObjectMemberIndent  = "\n      "
	nestedObjectClosingIndent = "\n    "
	valueLeadingSpace         = " "
)

type workspaceFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func workspacePath(primaryRoot string) string {
	return filepath.Join(primaryRoot, filepath.Base(primaryRoot)+".code-workspace")
}

func writeWorkspace(primaryRoot string, previous Manifest, manifest Manifest, projectsDirName string) error {
	data, err := renderWorkspace(primaryRoot, previous, manifest, projectsDirName)
	if err != nil {
		return err
	}
	return os.WriteFile(workspacePath(primaryRoot), data, 0o644)
}

func renderWorkspace(primaryRoot string, previous Manifest, manifest Manifest, projectsDirName string) ([]byte, error) {
	value, _, err := loadWorkspaceValue(workspacePath(primaryRoot))
	if err != nil {
		return nil, err
	}
	if err := mergeWorkspaceValue(&value, previous, manifest, projectsDirName); err != nil {
		return nil, err
	}
	return value.Pack(), nil
}

func loadWorkspaceValue(path string) (hujson.Value, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			value, parseErr := hujson.Parse([]byte(workspaceTemplate))
			if parseErr != nil {
				return hujson.Value{}, true, fmt.Errorf("parse workspace template: %w", parseErr)
			}
			return value, true, nil
		}
		return hujson.Value{}, false, fmt.Errorf("read workspace: %w", err)
	}

	value, err := hujson.Parse(data)
	if err != nil {
		return hujson.Value{}, false, fmt.Errorf("parse workspace as JSONC: %w", err)
	}
	return value, false, nil
}

func mergeWorkspaceValue(value *hujson.Value, previous Manifest, manifest Manifest, projectsDirName string) error {
	root, ok := value.Value.(*hujson.Object)
	if !ok {
		return fmt.Errorf("workspace root must be a JSON object")
	}

	currentFolders, err := workspaceFolders(manifest)
	if err != nil {
		return err
	}
	previousFolders, err := workspaceFolders(previous)
	if err != nil {
		return err
	}

	foldersArray := ensureTopLevelArrayMember(root, "folders")
	mergeFoldersArray(foldersArray, previousFolders, currentFolders)

	return nil
}

func workspaceFolders(manifest Manifest) ([]workspaceFolder, error) {
	folders := []workspaceFolder{
		{Name: filepath.Base(manifest.PrimaryRoot), Path: "."},
	}
	for _, entry := range manifest.Trusted {
		path := entry.MountPath
		if path == "" {
			return nil, fmt.Errorf("trusted attachment %q has empty mount_path", entry.RepoRef)
		}
		relativePath, err := workspaceRelativePath(manifest.PrimaryRoot, path)
		if err != nil {
			return nil, err
		}
		folders = append(folders, workspaceFolder{
			Name: filepath.Base(path),
			Path: relativePath,
		})
	}
	for _, entry := range manifest.External {
		relativePath, err := workspaceRelativePath(manifest.PrimaryRoot, entry.CheckoutPath)
		if err != nil {
			return nil, err
		}
		folders = append(folders, workspaceFolder{
			Name: filepath.Base(entry.CheckoutPath),
			Path: relativePath,
		})
	}

	return folders, nil
}

func trustedMountPath(primaryRoot string, projectsDirName string, repoName string) string {
	if projectsDirName == "." {
		return filepath.Join(primaryRoot, repoName)
	}
	return filepath.Join(primaryRoot, projectsDirName, repoName)
}

func workspaceRelativePath(primaryRoot string, targetPath string) (string, error) {
	relativePath, err := filepath.Rel(primaryRoot, targetPath)
	if err != nil {
		return "", fmt.Errorf("compute relative workspace path for %s: %w", targetPath, err)
	}
	if relativePath == "." {
		return ".", nil
	}
	return filepath.ToSlash(relativePath), nil
}

func mergeFoldersArray(arr *hujson.Array, previous []workspaceFolder, current []workspaceFolder) {
	currentByPath := make(map[string]workspaceFolder, len(current))
	for _, folder := range current {
		currentByPath[folder.Path] = folder
	}

	previousPaths := make(map[string]struct{}, len(previous))
	for _, folder := range previous {
		previousPaths[folder.Path] = struct{}{}
	}

	hadTrailingComma := arrayHasTrailingComma(arr)
	used := map[string]struct{}{}
	result := make([]hujson.Value, 0, len(arr.Elements)+len(current))
	for _, element := range arr.Elements {
		path, isFolderObject := folderElementPath(element)
		_, wasManaged := previousPaths[path]
		currentFolder, isManaged := currentByPath[path]
		if path == "." || wasManaged || isManaged {
			if !isManaged {
				continue
			}
			if _, seen := used[path]; seen {
				continue
			}
			if isFolderObject {
				updateFolderElement(&element, currentFolder)
			} else {
				replacement := newFolderElement(currentFolder)
				replacement.BeforeExtra = copyExtra(element.BeforeExtra)
				replacement.AfterExtra = copyExtra(element.AfterExtra)
				element = replacement
			}
			result = append(result, element)
			used[path] = struct{}{}
			continue
		}

		result = append(result, element)
	}

	for _, folder := range current {
		if _, ok := used[folder.Path]; ok {
			continue
		}
		result = append(result, newFolderElement(folder))
	}

	arr.Elements = result
	setArrayTrailingComma(arr, hadTrailingComma)
}

func ensureTopLevelArrayMember(root *hujson.Object, name string) *hujson.Array {
	member := ensureObjectMember(root, name, topLevelMemberIndent, newArray(arrayClosingIndent))
	array, ok := member.Value.Value.(*hujson.Array)
	if ok {
		if len(array.Elements) == 0 && array.AfterExtra == nil {
			array.AfterExtra = hujson.Extra(arrayClosingIndent)
		}
		return array
	}

	before := copyExtra(member.Value.BeforeExtra)
	after := copyExtra(member.Value.AfterExtra)
	member.Value = hujson.Value{
		BeforeExtra: preserveValueBeforeExtra(before),
		Value:       newArray(arrayClosingIndent),
		AfterExtra:  after,
	}
	return member.Value.Value.(*hujson.Array)
}

func ensureObjectMember(obj *hujson.Object, name string, memberIndent string, value hujson.ValueTrimmed) *hujson.ObjectMember {
	if _, member := findObjectMember(obj, name); member != nil {
		return member
	}

	member := hujson.ObjectMember{
		Name: hujson.Value{
			BeforeExtra: hujson.Extra(memberIndent),
			Value:       hujson.String(name),
		},
		Value: hujson.Value{
			BeforeExtra: hujson.Extra(valueLeadingSpace),
			Value:       value,
		},
	}
	obj.Members = append(obj.Members, member)
	return &obj.Members[len(obj.Members)-1]
}

func updateFolderElement(element *hujson.Value, folder workspaceFolder) {
	obj, ok := element.Value.(*hujson.Object)
	if !ok {
		return
	}
	ensureStringMember(obj, "name", folder.Name, nestedObjectMemberIndent)
	ensureStringMember(obj, "path", folder.Path, nestedObjectMemberIndent)
}

func ensureStringMember(obj *hujson.Object, name string, value string, memberIndent string) {
	if _, member := findObjectMember(obj, name); member != nil {
		member.Value.Value = hujson.String(value)
		return
	}

	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name: hujson.Value{
			BeforeExtra: hujson.Extra(memberIndent),
			Value:       hujson.String(name),
		},
		Value: hujson.Value{
			BeforeExtra: hujson.Extra(valueLeadingSpace),
			Value:       hujson.String(value),
		},
	})
}

func newFolderElement(folder workspaceFolder) hujson.Value {
	return hujson.Value{
		BeforeExtra: hujson.Extra(arrayElementIndent),
		Value: &hujson.Object{
			Members: []hujson.ObjectMember{
				newStringMember("name", folder.Name, nestedObjectMemberIndent),
				newStringMember("path", folder.Path, nestedObjectMemberIndent),
			},
			AfterExtra: hujson.Extra(nestedObjectClosingIndent),
		},
	}
}

func newStringMember(name string, value string, memberIndent string) hujson.ObjectMember {
	return hujson.ObjectMember{
		Name: hujson.Value{
			BeforeExtra: hujson.Extra(memberIndent),
			Value:       hujson.String(name),
		},
		Value: hujson.Value{
			BeforeExtra: hujson.Extra(valueLeadingSpace),
			Value:       hujson.String(value),
		},
	}
}

func newArray(closingIndent string) *hujson.Array {
	return &hujson.Array{AfterExtra: hujson.Extra(closingIndent)}
}

func folderElementPath(element hujson.Value) (string, bool) {
	obj, ok := element.Value.(*hujson.Object)
	if !ok {
		return "", false
	}
	path, ok := objectStringValue(obj, "path")
	return path, ok
}

func objectStringValue(obj *hujson.Object, name string) (string, bool) {
	_, member := findObjectMember(obj, name)
	if member == nil {
		return "", false
	}
	literal, ok := member.Value.Value.(hujson.Literal)
	if !ok || literal.Kind() != '"' {
		return "", false
	}
	return literal.String(), true
}

func findObjectMember(obj *hujson.Object, name string) (int, *hujson.ObjectMember) {
	for i := range obj.Members {
		memberName, ok := memberName(obj.Members[i])
		if ok && memberName == name {
			return i, &obj.Members[i]
		}
	}
	return -1, nil
}

func memberName(member hujson.ObjectMember) (string, bool) {
	literal, ok := member.Name.Value.(hujson.Literal)
	if !ok || literal.Kind() != '"' {
		return "", false
	}
	return literal.String(), true
}

func preserveValueBeforeExtra(extra hujson.Extra) hujson.Extra {
	if extra == nil {
		return hujson.Extra(valueLeadingSpace)
	}
	return extra
}

func copyExtra(extra hujson.Extra) hujson.Extra {
	if extra == nil {
		return nil
	}
	return append(hujson.Extra(nil), extra...)
}

func arrayHasTrailingComma(arr *hujson.Array) bool {
	if len(arr.Elements) == 0 {
		return false
	}
	return arr.Elements[len(arr.Elements)-1].AfterExtra != nil
}

func setArrayTrailingComma(arr *hujson.Array, enabled bool) {
	if len(arr.Elements) == 0 {
		return
	}
	last := &arr.Elements[len(arr.Elements)-1]
	if enabled {
		if last.AfterExtra == nil {
			last.AfterExtra = hujson.Extra{}
		}
		return
	}
	last.AfterExtra = nil
}

func decodeWorkspaceJSON(data []byte) (map[string]any, error) {
	standard, err := hujson.Standardize(data)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(standard, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
