<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type Job } from '$lib/api';

  let youtubeUrl = $state('');
  let device = $state<'auto' | 'cpu' | 'cuda'>('auto');
  let startSec = $state(0);
  let showAdvanced = $state(false);
  let submitting = $state(false);
  let jobs = $state<Job[]>([]);
  let loading = $state(true);
  let toast = $state<{ type: 'success' | 'error'; msg: string } | null>(null);
  let toastTimer: ReturnType<typeof setTimeout>;
  let pollInterval: ReturnType<typeof setInterval>;

  onMount(() => {
    fetchJobs();
    pollInterval = setInterval(fetchJobs, 2500);

    return () => {
      clearInterval(pollInterval);
    };
  });

  async function fetchJobs() {
    try {
      const result = await api.listJobs({ limit: 50 });
      jobs = result ?? [];
    } finally {
      loading = false;
    }
  }

  async function submit() {
    if (!youtubeUrl.trim()) return;
    submitting = true;
    try {
      const job = await api.createJob({
        youtube_url: youtubeUrl.trim(),
        device,
        start_sec: Math.max(0, Math.floor(startSec || 0)),
      });
      jobs = [job, ...jobs];
      youtubeUrl = '';
      showToast('success', 'Job queued. Processing will start shortly.');
    } catch (e: unknown) {
      showToast('error', e instanceof Error ? e.message : 'Failed to submit job.');
    } finally {
      submitting = false;
    }
  }

  async function cancelJob(id: string) {
    try {
      const updated = await api.cancelJob(id);
      jobs = jobs.map((j) => (j.id === id ? updated : j));
    } catch (e: unknown) {
      showToast('error', e instanceof Error ? e.message : 'Failed to cancel job.');
    }
  }

  function showToast(type: 'success' | 'error', msg: string) {
    clearTimeout(toastTimer);
    toast = { type, msg };
    toastTimer = setTimeout(() => (toast = null), 4000);
  }

  function formatMs(ms?: number): string {
    if (!ms) return '--';
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(1)}s`;
  }

  function friendlyDate(iso: string): string {
    return new Intl.DateTimeFormat('en-US', {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(iso));
  }

  function isActive(j: Job) {
    return j.state === 'queued' || j.state === 'running' || j.state === 'retrying';
  }

  const stateLabel: Record<string, string> = {
    queued: 'Queued',
    running: 'Running',
    retrying: 'Retrying',
    completed: 'Done',
    failed: 'Failed',
    cancelled: 'Cancelled',
  };
</script>

<div class="content-wrapper container">
  <section class="hero">
    <div class="submit-section-header">
      <h2>Submit</h2>
    </div>

    <div class="card submit-card">
      <form onsubmit={(e) => { e.preventDefault(); submit(); }}>
        <div class="form-row">
          <div class="input-wrapper">
            <span class="input-icon">></span>
            <input
              id="youtube-url"
              class="input input-url"
              type="url"
              bind:value={youtubeUrl}
              placeholder="https://youtube.com/watch?v=..."
              autocomplete="off"
              spellcheck="false"
              required
            />
          </div>

          <select id="device-select" class="input device-select" bind:value={device}>
            <option value="auto">Auto</option>
            <option value="cpu">CPU only</option>
            <option value="cuda">CUDA (GPU)</option>
          </select>
        </div>

        <button
          type="button"
          class="btn btn-ghost btn-sm advanced-toggle"
          onclick={() => (showAdvanced = !showAdvanced)}
          aria-expanded={showAdvanced}
        >
          <span class="advanced-arrow" aria-hidden="true">{showAdvanced ? '▲' : '▶'}</span>
          <span>Advanced</span>
        </button>

        {#if showAdvanced}
          <div class="form-row">
            <div class="input-wrapper">
              <label for="start-sec" class="field-label">Start from (sec)</label>
              <input id="start-sec" class="input" type="number" min="0" step="1" bind:value={startSec} />
            </div>
          </div>
        {/if}

        <div class="form-actions">
          <button id="submit-btn" class="btn btn-primary" type="submit" disabled={submitting || !youtubeUrl.trim()}>
            {#if submitting}
              <span class="spinner"></span> Submitting...
            {:else}
              Transcribe
            {/if}
          </button>
        </div>
      </form>
    </div>
  </section>

  <section class="jobs-section">
    <div class="section-header">
      <h2>
        Jobs
        {#if jobs.length > 0}
          <span class="count-badge">{jobs.length}</span>
        {/if}
      </h2>
      <button class="btn btn-ghost btn-sm" onclick={fetchJobs}>Refresh</button>
    </div>

    {#if loading}
      <div class="jobs-list">
        {#each [0, 1, 2] as _}
          <div class="card skeleton-card">
            <div class="skeleton" style="height:16px;width:60%;margin-bottom:0.75rem"></div>
            <div class="skeleton" style="height:12px;width:40%"></div>
          </div>
        {/each}
      </div>
    {:else if jobs.length === 0}
      <div class="empty-state">
        <div class="empty-icon">*</div>
        <p>No jobs yet. Paste a YouTube URL above to get started.</p>
      </div>
    {:else}
      <div class="jobs-list">
        {#each jobs as job (job.id)}
          <article class="card job-card" class:active={isActive(job)} class:done={job.state === 'completed'}>
            <div class="job-top">
              <div class="job-meta">
                <span class="badge badge-{job.state}">{stateLabel[job.state] ?? job.state}</span>
                <span class="job-engine">{job.engine} - {job.device.toUpperCase()}</span>
              </div>
              <span class="job-date">{friendlyDate(job.created_at)}</span>
            </div>

            {#if job.youtube_url}
              <a class="job-url" href={job.youtube_url} target="_blank" rel="noreferrer" title={job.youtube_url}>
                {job.youtube_url.length > 60 ? job.youtube_url.slice(0, 60) + '...' : job.youtube_url}
              </a>
            {/if}

            {#if isActive(job)}
              <div class="progress-bar" style="margin-top:0.75rem">
                <div class="progress-bar-fill"></div>
              </div>
            {/if}

            {#if job.total_ms}
              <div class="job-timings">
                <span>Download {formatMs(job.download_ms)}</span>
                <span>Infer {formatMs(job.inference_ms)}</span>
                <span>Total {formatMs(job.total_ms)}</span>
              </div>
            {/if}

            {#if job.error_message}
              <div class="job-error">
                <span class="job-error-code">{job.error_code}</span>
                {job.error_message}
              </div>
            {/if}

            {#if job.attempt > 1}
              <div class="attempt-info">Attempt {job.attempt} / {job.max_attempts}</div>
            {/if}

            <div class="job-actions">
              {#if job.state === 'completed'}
                <a id="download-{job.id}" class="btn btn-primary btn-sm" href={api.downloadMidi(job.id)} download="output.mid">
                  Download MIDI
                </a>
              {/if}
              {#if isActive(job)}
                <button id="cancel-{job.id}" class="btn btn-danger btn-sm" onclick={() => cancelJob(job.id)}>
                  Cancel
                </button>
              {/if}
            </div>
          </article>
        {/each}
      </div>
    {/if}
  </section>
</div>

{#if toast}
  <div class="toast toast-{toast.type}" role="alert">
    {toast.type === 'success' ? 'OK' : 'Error'}
    {toast.msg}
  </div>
{/if}

<style>
  .content-wrapper {
    min-height: calc(100dvh - var(--app-header-height));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .hero {
    text-align: center;
    margin-bottom: 0;
  }

  .submit-card { max-width: 100%; width: 100%; margin: 0 auto; }
  .submit-section-header {
    margin-bottom: 0.35rem;
    width: 100%;
    text-align: left;
  }
  .submit-section-header h2 {
    margin-bottom: 0.3rem;
  }
  .form-row {
    display: flex;
    gap: 0.75rem;
    flex-wrap: wrap;
    margin-bottom: 1rem;
  }
  .input-wrapper {
    position: relative;
    flex: 1;
    min-width: 200px;
  }
  .input-icon {
    position: absolute;
    left: 0.9rem;
    top: 50%;
    transform: translateY(-50%);
    font-size: 0.85rem;
    color: var(--text-muted);
    pointer-events: none;
  }
  .input-url { padding-left: 2.2rem; }
  .device-select { max-width: 180px; flex-shrink: 0; }
  .field-label {
    display: block;
    text-align: left;
    font-size: 0.78rem;
    color: var(--text-muted);
    margin-bottom: 0.35rem;
  }

  .advanced-toggle {
    display: flex;
    align-items: center;
    justify-content: flex-start;
    margin: 0 0 0.8rem;
    gap: 0.35rem;
    width: fit-content;
    text-align: left;
  }
  .advanced-arrow {
    width: 0.8rem;
    display: inline-flex;
    justify-content: center;
  }

  .form-actions {
    display: flex;
    justify-content: center;
    width: 100%;
  }
  #submit-btn {
    width: 100%;
    justify-content: center;
  }

  .spinner {
    display: inline-block;
    width: 14px;
    height: 14px;
    border: 2px solid rgba(255, 255, 255, 0.3);
    border-top-color: #fff;
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .jobs-section {
    padding-top: 0;
    margin-top: 30px;
  }
  .section-header {
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.65rem;
  }
  .section-header h2 {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .count-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--accent-dim);
    color: var(--accent-light);
    font-size: 0.75rem;
    font-weight: 600;
    width: 22px;
    height: 22px;
    border-radius: 50%;
  }

  .jobs-list {
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
  }

  .job-card {
    transition: border-color 0.2s, box-shadow 0.2s, background 0.2s;
  }
  .job-card.active {
    border-color: rgba(59, 130, 246, 0.25);
    background: rgba(59, 130, 246, 0.04);
  }
  .job-card.done {
    border-color: rgba(16, 185, 129, 0.2);
    background: rgba(16, 185, 129, 0.03);
  }

  .job-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-bottom: 0.6rem;
  }
  .job-meta {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .job-engine {
    font-size: 0.78rem;
    color: var(--text-muted);
    font-family: 'JetBrains Mono', monospace;
  }
  .job-date {
    font-size: 0.78rem;
    color: var(--text-muted);
  }

  .job-url {
    display: block;
    font-size: 0.84rem;
    color: var(--accent-light);
    text-decoration: none;
    word-break: break-all;
    margin-bottom: 0.25rem;
    transition: color 0.15s;
  }
  .job-url:hover {
    color: #fff;
    text-decoration: underline;
  }

  .job-timings {
    display: flex;
    gap: 1.25rem;
    font-size: 0.78rem;
    color: var(--text-muted);
    margin-top: 0.6rem;
  }

  .job-error {
    margin-top: 0.6rem;
    padding: 0.5rem 0.75rem;
    background: rgba(239, 68, 68, 0.07);
    border: 1px solid rgba(239, 68, 68, 0.15);
    border-radius: var(--radius-sm);
    font-size: 0.82rem;
    color: var(--red);
  }
  .job-error-code {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.75rem;
    background: rgba(239, 68, 68, 0.12);
    padding: 1px 5px;
    border-radius: 3px;
    margin-right: 0.4rem;
  }

  .attempt-info {
    font-size: 0.78rem;
    color: var(--yellow);
    margin-top: 0.4rem;
  }

  .job-actions {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.85rem;
    flex-wrap: wrap;
  }

  .empty-state {
    text-align: center;
    padding: 4rem 1rem;
    color: var(--text-muted);
  }
  .empty-icon {
    font-size: 3rem;
    margin-bottom: 1rem;
    opacity: 0.3;
    filter: drop-shadow(0 0 20px var(--accent));
  }
  .skeleton-card {
    pointer-events: none;
    opacity: 0.6;
  }
</style>
