// Typesetting for running text that wraps at many widths.

/**
 * Binds a paragraph's last three words with no-break spaces, so its last
 * line always carries at least three words: no orphan, no runt like
 * "to paste." stranded under a full line. Works the same in every engine,
 * unlike `text-wrap: pretty`.
 */
export const unrunt = (text: string): string => text.replace(/ (\S+) (\S+)$/u, " $1 $2");

/** A run of caption text; `term` marks an Arabic term (an `<i>`). */
export interface Run {
  text: string;
  term: boolean;
}

/**
 * Splits plain caption text into runs: `_word_` marks an Arabic term (see
 * README, Voice). Shared by the page and the tour script, which swaps
 * captions in the browser.
 */
export const runs = (text: string): Run[] =>
  text.split(/_([^_]+)_/u).flatMap((t, i) => (t ? [{ text: t, term: i % 2 === 1 }] : []));
