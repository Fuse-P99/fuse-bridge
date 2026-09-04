<script>
  // Renders a guide post fetched from Discord: the text through the
  // discordmd.js block parser (pure text nodes — author content can never
  // inject markup), then attachments — images inline, videos with native
  // controls, anything else as a download link. External links open in the
  // system browser via target=_blank (never discord:// — deep links don't
  // work for admin-run Discord).
  import { parseBlocks } from "./discordmd.js";

  export let text = "";
  export let media = []; // [{kind: "image"|"video"|"file", url, name}]

  $: blocks = parseBlocks(text);
</script>

<div class="dm">
  {#each blocks as b}
    {#if b.type === "code"}
      <pre class="dm-code">{b.text}</pre>
    {:else if b.type === "gap"}
      <div class="dm-gap"></div>
    {:else}
      <div
        class="dm-line"
        class:dm-h1={b.type === "h1"}
        class:dm-h2={b.type === "h2"}
        class:dm-h3={b.type === "h3"}
        class:dm-quote={b.type === "quote"}
        class:dm-li={b.type === "li"}
      >
        {#if b.type === "li"}<span class="dm-bullet"
            >{b.num ? b.num + "." : "•"}</span
          >{/if}
        {#each b.segs as s}
          {#if s.t === "bold"}<b>{s.txt}</b>
          {:else if s.t === "italic"}<i>{s.txt}</i>
          {:else if s.t === "under"}<u>{s.txt}</u>
          {:else if s.t === "strike"}<s>{s.txt}</s>
          {:else if s.t === "code"}<code>{s.txt}</code>
          {:else if s.t === "link"}<a
              href={s.url}
              target="_blank"
              rel="noreferrer">{s.txt}</a
            >
          {:else}{s.txt}{/if}
        {/each}
      </div>
    {/if}
  {/each}

  {#each media as m}
    {#if m.kind === "image"}
      <img class="dm-img" src={m.url} alt={m.name || "guide image"} />
    {:else if m.kind === "video"}
      <!-- svelte-ignore a11y-media-has-caption -->
      <video class="dm-video" src={m.url} controls preload="metadata"></video>
    {:else}
      <a class="dm-file" href={m.url} target="_blank" rel="noreferrer"
        >📎 {m.name || "attachment"}</a
      >
    {/if}
  {/each}
</div>

<style>
  .dm {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 12.5px;
    line-height: 1.5;
    color: var(--text-primary);
    overflow-wrap: anywhere;
  }
  .dm-line {
    white-space: pre-wrap;
  }
  .dm-gap {
    height: 8px;
  }
  .dm-h1,
  .dm-h2,
  .dm-h3 {
    font-weight: 700;
    margin-top: 4px;
  }
  .dm-h1 {
    font-size: 16px;
  }
  .dm-h2 {
    font-size: 14.5px;
  }
  .dm-h3 {
    font-size: 13px;
  }
  .dm-quote {
    border-left: 3px solid var(--border-hover);
    padding-left: 8px;
    color: var(--text-secondary);
  }
  .dm-li {
    padding-left: 14px;
    display: flex;
    gap: 6px;
  }
  .dm-bullet {
    color: var(--text-muted);
    flex-shrink: 0;
  }
  .dm-code {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 7px 9px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    overflow-x: auto;
    margin: 3px 0;
  }
  code {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
    font-family: var(--font-mono);
    font-size: 11.5px;
  }
  a {
    color: var(--accent);
  }
  .dm-img {
    max-width: 100%;
    border-radius: 4px;
    border: 1px solid var(--border);
    margin-top: 6px;
  }
  .dm-video {
    max-width: 100%;
    border-radius: 4px;
    border: 1px solid var(--border);
    margin-top: 6px;
    background: #000;
  }
  .dm-file {
    margin-top: 6px;
    font-size: 12px;
  }
</style>
