// Package private creates and checks the directories and files that hold whatsapp-mcp's data, so that only
// the user running whatsapp-mcp can reach them.
//
// A private directory is one that is not a symbolic link or other reparse point and is owned by this user.
// On Unix no group or other permission bits are set, and its parent cannot be written by other users unless
// it has the sticky bit (like /tmp). On Windows every entry in its access list that allows access names this
// user, SYSTEM or the Administrators group. A private file is a regular file with a single link and the same
// owner. On Unix group and others cannot write it (they cannot reach it through a private directory anyway);
// on Windows its access list follows the directory rule.
//
// The directories above the data directory are trusted. Checks are made on an open handle wherever the
// platform allows it, so a path swapped between the check and the use is caught. Nothing here changes the
// permissions of a directory or file that already exists: a failed check explains the fix instead.
package private
