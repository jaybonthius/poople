import { rocket } from 'datastar'

type Outcome = 'shared' | 'copied' | 'failed'

// Share button for the finish sheet. The server renders the spoiler-free
// result as the text prop; the component shares it with the Web Share API when
// the browser has one, otherwise copies it, shows the outcome on the button and
// announces it, and dispatches a bubbling "share-result" event whose detail is
// { outcome }.
rocket('share-result', {
  mode: 'light',
  props: ({ string }) => ({
    text: string.docs({ description: 'Spoiler-free result text to share or copy.' }),
  }),
  manifest: {
    events: [{
      name: 'share-result',
      kind: 'custom-event',
      bubbles: true,
      description: 'Fired after sharing; detail.outcome is "shared", "copied" or "failed".',
    }],
  },
  setup: ({ $$, action, cleanup, host, props }) => {
    $$.outcome = ''
    let reset = 0
    const settle = (outcome: Outcome) => {
      $$.outcome = outcome
      host.dispatchEvent(new CustomEvent('share-result', { bubbles: true, detail: { outcome } }))
      clearTimeout(reset)
      reset = window.setTimeout(() => { $$.outcome = '' }, 2500)
    }
    action('share', async () => {
      // The site's own address ends the result, so friends can tap through to play.
      const text = `${props.text}\n${location.origin}/`
      if (typeof navigator.share === 'function' && navigator.canShare?.({ text }) !== false) {
        try {
          await navigator.share({ text })
          settle('shared')
          return
        } catch (error) {
          // The player closed the share sheet: nothing to report.
          if (error instanceof DOMException && error.name === 'AbortError') return
        }
      }
      try {
        await navigator.clipboard.writeText(text)
        settle('copied')
      } catch {
        settle('failed')
      }
    })
    cleanup(() => clearTimeout(reset))
  },
  render: ({ html }) => html`
    <button type="button" class="button button--primary" data-on:click="@share()">
      <svg class="icon" aria-hidden="true"><use href="#i-share"></use></svg>
      <span data-text="$$outcome === 'copied' ? 'Copied!' : $$outcome === 'failed' ? 'Couldn’t copy' : 'Share result'">Share result</span>
    </button>
    <span class="visually-hidden" role="status" data-text="$$outcome === 'copied' ? 'Result copied to the clipboard' : $$outcome === 'failed' ? 'Could not copy the result' : ''"></span>
  `,
})
