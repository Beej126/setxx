package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// isAdmin checks if the current process has administrator rights
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}

	token := windows.GetCurrentProcessToken()
	isMember, err := token.IsMember(sid)
	return err == nil && isMember
}

// elevateIfNeeded restarts the program with admin privileges if not already elevated
func elevateIfNeeded() {
	if isAdmin() {
		return // Already running as admin, no need to restart
	}

	fmt.Println("Press ENTER to continue with required elevation...")
	bufio.NewReader(os.Stdin).ReadBytes('\r') // Read until newline is encountered

	cmd := exec.Command(os.Args[0])
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CmdLine:       "",
		CreationFlags: windows.CREATE_NEW_CONSOLE | windows.DETACHED_PROCESS,
	}
	args := strings.Join(os.Args[1:], " ") // Join all arguments into a single string
	err := windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(os.Args[0]), windows.StringToUTF16Ptr(args), nil, windows.SW_HIDE)

	if err != nil {
		fmt.Println("Failed to restart with elevated privileges:", err)
		os.Exit(1)
	}

	os.Exit(0) // Exit current instance, elevated version takes over
}

// broadcastWMSettingChange notifies all applications that an environment variable was updated
func broadcastWMSettingChange(varName string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	sendMessage := user32.NewProc("SendMessageTimeoutW")

	env := windows.StringToUTF16Ptr("Environment")
	sendMessage.Call(
		0xFFFF,                       // HWND_BROADCAST (sends to all windows)
		0x1A,                         // WM_SETTINGCHANGE
		0,                            // wParam (unused)
		uintptr(unsafe.Pointer(env)), // lParam (Environment variable changed)
		0x2,                          // SMTO_ABORTIFHUNG
		5000,                         // Timeout (milliseconds)
		0,                            // Result (unused)
	)
}

// getRegistryValue fetches the environment variable from the registry or initializes it if missing
func getRegistryValue(scope registry.Key, varName string) (string, error) {
	k, err := registry.OpenKey(scope, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	val, _, err := k.GetStringValue(varName)
	if err != nil {
		fmt.Println(varName, "does not exist, creating entry...")
		err = k.SetStringValue(varName, "")
		if err != nil {
			return "", err
		}
		return "", nil
	}
	return val, nil
}

// updateRegistryValue updates the environment variable in the registry
func updateRegistryValue(scope registry.Key, varName, newValue string) error {
	k, err := registry.OpenKey(scope, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	err = k.SetStringValue(varName, newValue)
	if err != nil {
		return err
	}

	broadcastWMSettingChange(varName)
	return nil
}

// checkIfExists checks if a directory or value is already present
func checkIfExists(envValue, entry string) bool {
	entries := strings.Split(envValue, ";")
	for _, e := range entries {
		if strings.EqualFold(e, entry) {
			return true
		}
	}
	return false
}

// addToVariable safely adds an entry to the environment variable
func addToVariable(scope registry.Key, varName, entry string, isUppercase, isSystem bool) error {
	if isUppercase {
		entry = strings.ToUpper(entry)
	}

	envValue, err := getRegistryValue(scope, varName)
	if err != nil {
		return err
	}

	if checkIfExists(envValue, entry) {
		fmt.Println("Entry already exists in", varName)
		return nil
	}

	newValue := envValue + ";" + entry
	err = updateRegistryValue(scope, varName, newValue)
	if err != nil {
		return err
	}

	scopeLabel := "User"
	if isSystem {
		scopeLabel = "System"
	}

	fmt.Println("Added to", scopeLabel, varName, "successfully")
	return nil
}

// removeFromVariable safely removes an entry from the environment variable
func removeFromVariable(scope registry.Key, varName, entry string, isSystem bool) error {
	envValue, err := getRegistryValue(scope, varName)
	if err != nil {
		return err
	}

	if !checkIfExists(envValue, entry) {
		fmt.Println("Entry not found in", varName)
		return nil
	}

	entries := strings.Split(envValue, ";")
	var newEntries []string
	for _, e := range entries {
		if !strings.EqualFold(e, entry) {
			newEntries = append(newEntries, e)
		}
	}

	newValue := strings.Join(newEntries, ";")
	err = updateRegistryValue(scope, varName, newValue)
	if err != nil {
		return err
	}

	scopeLabel := "User"
	if isSystem {
		scopeLabel = "System"
	}

	fmt.Println("Removed from", scopeLabel, varName, "successfully")
	return nil
}

func main() {
	var systemScope, uppercase bool
	flag.BoolVar(&systemScope, "s", false, "Modify system variable instead of user variable")
	flag.BoolVar(&uppercase, "u", false, "Uppercase value before adding")
	flag.Parse()

	args := flag.Args()
	if len(args) < 3 {
		fmt.Println("Usage: program -s -u [add/remove] <entry> <variable>")
		fmt.Println("       -s for system variables otherwise defaults to user")
		fmt.Println("       -u for uppercasing the value before adding")
		os.Exit(1)
	}

	action := args[0]
	entry := args[1]
	varName := args[2]

	// Ensure elevation for system variable modification
	if systemScope {
		elevateIfNeeded()
	}

	scope := registry.CURRENT_USER
	scopeLabel := "User"

	if systemScope {
		var err error
		scope, err = registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			fmt.Println("Error accessing system registry:", err)
			os.Exit(1)
		}
		scopeLabel = "System"
	}

	fmt.Println("Modifying", scopeLabel, "environment variable:", varName)

	switch action {
	case "add":
		if err := addToVariable(scope, varName, entry, uppercase, systemScope); err != nil {
			fmt.Println("Error adding to", varName+":", err)
		}
	case "remove":
		if err := removeFromVariable(scope, varName, entry, systemScope); err != nil {
			fmt.Println("Error removing from", varName+":", err)
		}
	default:
		fmt.Println("Invalid action. Use 'add' or 'remove'.")
	}
}
