// The success mark shown after the service accepted something the human confirmed (see DoneMark.svelte).
export const done = $state({ n: 0, word: '', bad: false });
let timer: ReturnType<typeof setTimeout> | undefined;

/** `word` says what happened; `bad` draws it in the danger colour (a rejection, a removal). */
export function showDone(word: string, bad = false) {
  clearTimeout(timer);
  done.n++;
  done.word = word;
  done.bad = bad;
  timer = setTimeout(hideDone, 2100); // the fade in motion.css ends here
}

export function hideDone() {
  clearTimeout(timer);
  done.word = '';
}
