import { toast } from 'svelte-sonner';

/** Copy an id to the clipboard and say so in a toast. Ids are not secrets. */
export async function copyText(value: string, message = `Copied ${value}`) {
  try {
    await navigator.clipboard.writeText(value);
    toast.success(message);
  } catch {
    toast.error('Could not copy. Select the text and copy it by hand.');
  }
}
