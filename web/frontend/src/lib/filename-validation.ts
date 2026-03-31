/**
 * Filesystem-safe filename validation utilities.
 *
 * Mirrors the backend `SafeFilename()` function in `pkg/slug/safe_filename.go`.
 * Characters unsafe on macOS/Windows/Linux are stripped as the user types
 * (Obsidian-style prevention).
 */

/** Characters that are invalid in filenames on macOS, Windows, or Linux. */
const UNSAFE_CHARS = /[:/\\<>|"?*\x00]/g;

/** Collapse multiple consecutive spaces into one. */
const MULTI_SPACE = /  +/g;

/**
 * Strips filesystem-unsafe characters from a string, collapses whitespace,
 * and trims leading/trailing dots and spaces.
 *
 * This is the frontend equivalent of the Go `SafeFilename()` function.
 * Use it to sanitize title/name inputs before sending to the backend.
 */
export function sanitizeFilename(value: string): string {
  return value
    .replace(UNSAFE_CHARS, "")
    .replace(MULTI_SPACE, " ");
}

/**
 * Returns true if the character is unsafe for filenames.
 * Use this to prevent individual keystrokes in input handlers.
 */
export function isUnsafeChar(char: string): boolean {
  return UNSAFE_CHARS.test(char);
}

/**
 * React onChange handler helper that strips unsafe characters from an input
 * value in real-time. Returns the sanitized value.
 *
 * Usage:
 * ```tsx
 * const [title, setTitle] = useState("");
 * <Input
 *   value={title}
 *   onChange={(e) => setTitle(sanitizeInputValue(e.target.value))}
 * />
 * ```
 */
export function sanitizeInputValue(value: string): string {
  return value.replace(UNSAFE_CHARS, "");
}
