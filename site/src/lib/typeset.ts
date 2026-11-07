// Typesetting for running text that wraps at many widths.

/**
 * Binds a paragraph's last three words with no-break spaces, so its last
 * line always carries at least three words: no orphan, no runt like
 * "to paste." stranded under a full line. Works the same in every engine,
 * unlike `text-wrap: pretty`.
 */
export const unrunt = (text: string): string => text.replace(/ (\S+) (\S+)$/u, " $1 $2");
