# setxx

A robust Windows environment variables tool that sets global registry entries and broadcasts changes to all running applications.

## Usage

```
setxx v1.0.0: an environment variables tool

- sets global Windows environment variable registry entries
- and broadcasts the standard Windows wide message to signal environment changes to running applications

Basic usage:
  setxx <variable> <value>            # set/replace entire variable
  setxx -add <variable> <entry>       # add entry to path-like variable (semicolon delimited)
  setxx -remove <variable> <entry>    # remove entry from path-like variable
  setxx -remove <variable>            # delete entire variable

Options:
    -system    = system variable (default = user)
    -sure      = skip confirmation prompt on destructive actions
    -demo      = open new cmd window to display updated variable
    -demosplit = open new cmd window to display both system and user values of the variable
    -debug     = show debug information

  with -add:
    -top        = add to beginning of list (default = end)
    -upper      = uppercase the value (handy for PATHEXT =)
    -before XYZ = insert before this existing entry (moves if already exists)

Examples:
       setxx MYVAR "Hello World"        # set MYVAR to Hello World
       setxx -add PATH C:\NewPath       # add C:\NewPath to PATH
       setxx -remove PATH C:\OldPath    # remove C:\OldPath from PATH
       setxx -remove TEMP_VAR           # delete entire TEMP_VAR
       setxx -sure PATH "C:\Only\This"  # replace PATH without prompt
   PS> setxx -add PATH $(pwd).Path      # add current folder to PATH in PowerShell
  CMD> setxx -add PATH %cd%             # add current folder to PATH in Command Prompt
```

## Features

- **Robust argument parsing** with flexible flag positioning
- **Environment variable management** for both user and system scopes
- **Safe operations** with confirmation prompts for destructive PATH modifications
- **Demo windows** to visualize changes (single scope or split view comparing both scopes)
- **Position control** for adding entries (top, end, or before a specific entry)
- **Text transformation** with optional uppercase conversion
- **Debug mode** for troubleshooting
- **Automatic elevation** handling for system-wide changes
