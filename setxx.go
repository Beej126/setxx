package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
func addToVariable(scope registry.Key, varName, entry string, isUppercase, addToTop bool, beforeEntry, scopeLabel string) error {
	if isUppercase {
		entry = strings.ToUpper(entry)
	}

	envValue, err := getRegistryValue(scope, varName)
	if err != nil {
		return err
	}

	entries := strings.Split(envValue, ";")

	// Remove the entry if it already exists (for repositioning)
	var filteredEntries []string
	entryExists := false
	for _, e := range entries {
		if !strings.EqualFold(e, entry) {
			filteredEntries = append(filteredEntries, e)
		} else {
			entryExists = true
		}
	}

	var newEntries []string

	if beforeEntry != "" {
		// Insert before the specified entry
		beforeFound := false
		for _, e := range filteredEntries {
			if strings.EqualFold(e, beforeEntry) {
				newEntries = append(newEntries, entry)
				beforeFound = true
			}
			newEntries = append(newEntries, e)
		}
		// If beforeEntry wasn't found, add to the end
		if !beforeFound {
			newEntries = append(newEntries, entry)
			fmt.Printf("Warning: '%s' not found, added '%s' to end instead\n", beforeEntry, entry)
		}
	} else if addToTop {
		// Add to beginning
		newEntries = append([]string{entry}, filteredEntries...)
	} else {
		// Add to end (default)
		newEntries = append(filteredEntries, entry)
	}

	newValue := strings.Join(newEntries, ";")
	err = updateRegistryValue(scope, varName, newValue)
	if err != nil {
		return err
	}

	// Also update current process environment
	os.Setenv(varName, newValue)

	if entryExists {
		fmt.Printf("Moved %s in %s:%s", entry, scopeLabel, varName)
	} else {
		fmt.Printf("Added to %s:%s", scopeLabel, varName)
	}
	return nil
}

// removeFromVariable safely removes an entry from the environment variable
func removeFromVariable(scope registry.Key, varName, entry, scopeLabel string) error {
	envValue, err := getRegistryValue(scope, varName)
	if err != nil {
		return err
	}

	if !checkIfExists(envValue, entry) {
		fmt.Println("Entry not found in", varName)
		return fmt.Errorf("entry_not_found_silent")
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

	// Also update current process environment
	os.Setenv(varName, newValue)

	fmt.Printf("Removed from %s:%s", scopeLabel, varName)
	return nil
}

// deleteVariable completely removes the environment variable
func deleteVariable(scope registry.Key, varName, scopeLabel string) error {
	k, err := registry.OpenKey(scope, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	err = k.DeleteValue(varName)
	if err != nil {
		return err
	}

	// Also update current process environment
	os.Unsetenv(varName)

	broadcastWMSettingChange(varName)
	fmt.Printf("Deleted %s:%s", scopeLabel, varName)
	return nil
}

// setVariable sets the entire environment variable to the specified value
func setVariable(scope registry.Key, varName, value string, isUppercase bool, scopeLabel string) error {
	if isUppercase {
		value = strings.ToUpper(value)
	}

	err := updateRegistryValue(scope, varName, value)
	if err != nil {
		return err
	}

	// Also update current process environment
	os.Setenv(varName, value)

	fmt.Printf("Set %s:%s", scopeLabel, varName)
	return nil
}

// launchDemoWindow opens a new cmd.exe window to display the environment variable
func launchDemoWindow(varName, scopeLabel string) {
	// Create a command that displays the variable and pauses
	cmdLine := fmt.Sprintf(`@echo off & echo, & echo %s:%s=%%%s%% & echo, & pause`, scopeLabel, varName, varName)

	// Use cmd.exe start to launch a new independent command window with fresh environment.
	// This approach is preferred over direct Go exec because:
	// 1. The 'start' command creates a proper detached window that behaves like a normal cmd prompt
	// 2. The new process gets a fresh environment from the registry (shows our updated values)
	// 3. Window management (title bar, resize, etc.) is handled automatically by Windows
	// 4. Process independence - the demo window survives even if our program exits
	cmd := exec.Command("cmd.exe", "/c", "start", "cmd.exe", "/c", cmdLine)
	cmd.Run()
}

// main is the entry point for the program
func main() {
	var systemScope, uppercase, addToTop, debug, sure, demo bool
	var beforeEntry string
	var flagAdd, flagRemove bool

	// Success message for registry updates
	const successMessage = " successfully in the Registry. Open new terminals to see changes. Nested terminals, like inside of VSCode, will require restarting that parent process."

	// First pass: manually extract and reorder arguments to put flags first
	var flags []string
	var nonFlags []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			// Check if this flag expects a value (like -before)
			if arg == "-before" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // Skip the next argument as it's the value for -before
				flags = append(flags, args[i])
			}
		} else {
			nonFlags = append(nonFlags, arg)
		}
	}

	// Combine flags first, then non-flag arguments
	reorderedArgs := append(flags, nonFlags...)

	// Second pass: use flag parser on reordered arguments

	fs := flag.NewFlagSet("setxx", flag.ExitOnError)
	fs.BoolVar(&systemScope, "system", false, "Modify system variable instead of user variable")
	fs.BoolVar(&uppercase, "upper", false, "Uppercase value before adding")
	fs.BoolVar(&addToTop, "top", false, "Add entry to the beginning instead of the end")
	fs.StringVar(&beforeEntry, "before", "", "Insert entry before this existing entry")
	fs.BoolVar(&debug, "debug", false, "Show debug information for troubleshooting")
	fs.BoolVar(&sure, "sure", false, "Skip confirmation prompts for destructive operations")
	fs.BoolVar(&demo, "demo", false, "Open new cmd window to display updated variable after success")
	fs.BoolVar(&flagAdd, "add", false, "Add entry to a path-like variable (semicolon delimited)")
	fs.BoolVar(&flagRemove, "remove", false, "Remove entry from a path-like variable, or delete variable if only variable name is given")

	fs.Parse(reorderedArgs)
	parsedArgs := fs.Args()

	// Optional debug output
	if debug {
		fmt.Printf("DEBUG: Raw os.Args: %v\n", os.Args)
		fmt.Printf("DEBUG: Reordered args: %v\n", reorderedArgs)
		fmt.Printf("DEBUG: Parsed flags - system=%t, upper=%t, top=%t, before='%s', debug=%t, sure=%t, demo=%t\n", systemScope, uppercase, addToTop, beforeEntry, debug, sure, demo)
		fmt.Printf("DEBUG: Remaining args after flag parsing: %v\n", parsedArgs)
		fmt.Printf("DEBUG: len(args)=%d\n", len(parsedArgs))
	}

	if len(parsedArgs) < 2 {
		// Extract just the program name without path or extension
		exeName := filepath.Base(os.Args[0])
		if ext := filepath.Ext(exeName); ext != "" {
			exeName = strings.TrimSuffix(exeName, ext)
		}
		fmt.Println()
		fmt.Println("** PERMANENT environment variables tool **")
		fmt.Println("- sets global Windows environment variable registry entries")
		fmt.Println("- and broadcasts the standard Windows wide message to signal environment changes to running applications")
		fmt.Println()
		fmt.Printf("Basic usage:")
		fmt.Println()
		fmt.Printf("  %s <variable> <value>            # set/replace entire variable\n", exeName)
		fmt.Printf("  %s -add <variable> <entry>       # add entry to path-like variable (semicolon delimited)\n", exeName)
		fmt.Printf("  %s -remove <variable> <entry>    # remove entry from path-like variable\n", exeName)
		fmt.Printf("  %s -remove <variable>            # delete entire variable\n", exeName)
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("    -system = system variable (default = user)")
		fmt.Println("    -sure   = skip confirmation prompt on destructive actions")
		fmt.Println("    -demo   = open new cmd window to display updated variable")
		fmt.Println("    -debug  = show debug information")
		fmt.Println()
		fmt.Println("  with -add:")
		fmt.Println("    -top        = add to beginning of list (default = end)")
		fmt.Println("    -upper      = uppercase the value (handy for PATHEXT =)")
		fmt.Println("    -before XYZ = insert before this existing entry (moves if already exists)")
		fmt.Println()
		fmt.Println("Examples:")
		fmt.Printf("       %s MYVAR \"Hello World\"        # set MYVAR to Hello World\n", exeName)
		fmt.Printf("       %s -add PATH C:\\NewPath       # add C:\\NewPath to PATH\n", exeName)
		fmt.Printf("       %s -remove PATH C:\\OldPath    # remove C:\\OldPath from PATH\n", exeName)
		fmt.Printf("       %s -remove TEMP_VAR           # delete entire TEMP_VAR\n", exeName)
		fmt.Printf("       %s -sure PATH \"C:\\Only\\This\"  # replace PATH without prompt\n", exeName)
		fmt.Printf("   PS> %s -add PATH $(pwd).Path      # add current folder to PATH in PowerShell\n", exeName)
		fmt.Printf("  CMD> %s -add PATH %%cd%%             # add current folder to PATH in Command Prompt\n", exeName)
		fmt.Println()
		os.Exit(1)
	}

	// Determine action and arguments based on input
	var action, varName, entry string

	if flagAdd && flagRemove {
		fmt.Println("Error: Cannot use both -add and -remove flags at the same time.")
		os.Exit(1)
	}

	if flagAdd {
		if len(parsedArgs) != 2 {
			fmt.Println("Error: -add requires exactly 2 arguments: <variable> <entry>")
			os.Exit(1)
		}
		action = "add"
		varName = parsedArgs[0]
		entry = parsedArgs[1]
	} else if flagRemove {
		if len(parsedArgs) == 1 {
			action = "remove"
			varName = parsedArgs[0]
			entry = "" // delete entire variable
		} else if len(parsedArgs) == 2 {
			action = "remove"
			varName = parsedArgs[0]
			entry = parsedArgs[1]
		} else {
			fmt.Println("Error: -remove requires 1 or 2 arguments: <variable> [entry]")
			os.Exit(1)
		}
	} else {
		// No -add or -remove flag: default to set
		if len(parsedArgs) != 2 {
			fmt.Printf("Error: Expected 2 arguments for set: <variable> <value>. Got %d.\n", len(parsedArgs))
			os.Exit(1)
		}
		action = "set"
		varName = parsedArgs[0]
		entry = parsedArgs[1]
	}

	// For set action or remove entire variable, check if PATH is in variable name and prompt for confirmation
	if (action == "set" || (action == "remove" && entry == "")) && !sure && strings.Contains(strings.ToUpper(varName), "PATH") {
		operation := "overwrite"
		if action == "remove" {
			operation = "delete"
		}
		fmt.Printf("Warning: This will completely %s %s. Are you sure? (y/N): ", operation, varName)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Operation cancelled.")
			os.Exit(0)
		}
	}

	// Validate flag usage based on action
	if action == "remove" && (uppercase || addToTop || beforeEntry != "") {
		fmt.Println("Error: -upper, -top, and -before options cannot be used with 'remove' action")
		os.Exit(1)
	}

	if (action == "set" || action == "remove") && (addToTop || beforeEntry != "") {
		fmt.Println("Error: -top and -before options can only be used with 'add' action")
		os.Exit(1)
	}

	// Ensure elevation for system variable modification
	if systemScope {
		elevateIfNeeded()
	}

	scope := registry.CURRENT_USER
	scopeLabel := "USER"
	if systemScope {
		var err error
		scope, err = registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			fmt.Println("Error accessing system registry:", err)
			os.Exit(1)
		}
		scopeLabel = "SYSTEM"
	}

	var err error
	var operation string

	switch action {
	case "set":
		err = setVariable(scope, varName, entry, uppercase, scopeLabel)
		operation = "setting"
	case "add":
		err = addToVariable(scope, varName, entry, uppercase, addToTop, beforeEntry, scopeLabel)
		operation = "adding to"
	case "remove":
		if entry == "" {
			// Remove entire variable
			err = deleteVariable(scope, varName, scopeLabel)
			operation = "deleting"
		} else {
			// Remove specific entry from variable
			err = removeFromVariable(scope, varName, entry, scopeLabel)
			operation = "removing from"
		}
	default:
		fmt.Println("Invalid action. Use '-add', '-remove', or provide just <variable> <value> to set.")
		os.Exit(1)
	}

	if err != nil {
		// Suppress all output and exit 0 if entry not found for remove
		if action == "remove" && err.Error() == "entry_not_found_silent" {
			os.Exit(0)
		}
		fmt.Printf("Error %s %s: %v\n", operation, varName, err)
		os.Exit(1)
	} else {
		fmt.Println(successMessage)
		if demo {
			// Wait for Windows to propagate the environment variable change
			// before launching the demo window, to ensure the new value is visible.
			time.Sleep(1000 * time.Millisecond)
			launchDemoWindow(varName, scopeLabel)
		}
	}
}
