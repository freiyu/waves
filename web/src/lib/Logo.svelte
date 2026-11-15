<script lang="ts">
  // The wordmark: the traced mark and the name "waves" as paths (no font file), drawn as two separate pieces so the rail can keep the mark
  // and fold the name away. The letters take the text colour, so the light and the dark theme share one asset. The file is a static,
  // trusted asset of this build.
  import lockup from '../assets/logo-lockup.svg?raw';
  const H = 24; // rendered height in px (the source is 30 high)
  const piece = (svg: string, x: number, w: number) => svg.replace(/width="125" height="30" viewBox="0 0 125 30"/, `width="${(w * H) / 30}" height="${H}" viewBox="${x} 0 ${w} 30"`);
  const text = lockup.match(/<path[^>]*fill="currentColor"[^>]*\/>/)![0];
  const mark = piece(lockup.replace(text, ''), 0, 50);
  // The name carries no gradient: its copy drops the shared definitions, so no id appears twice on the page.
  const word = piece(lockup.replace(/<path[^>]*fill="url\(#paint\d_linear[^>]*\/>/g, '').replace(/<defs>[\s\S]*<\/defs>/, '').replace(/ clip-path="url\(#clip0[^"]*\)"/, ''), 52, 73);
</script>

<span class="brand-sm" style="color: var(--text)" role="img" aria-label="waves"><span class="lg-mark">{@html mark}</span><span class="lg-word">{@html word}</span></span>
