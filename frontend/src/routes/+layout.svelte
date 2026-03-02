<script lang="ts">
  import '../app.css';
  import { api } from '$lib/api';
  import { onMount } from 'svelte';

  let { children } = $props();
  let serverOnline = $state(false);
  let backendStatusTitle = $state('Check backend status');
  let headerEl: HTMLElement | null = null;
  let healthTimer: ReturnType<typeof setInterval> | null = null;
  let runtimeBannerHideTimer: ReturnType<typeof setTimeout> | null = null;
  let runtimeBootstrap = $state<{
    status: 'running' | 'done' | 'error';
    stage: string;
    message: string;
  } | null>(null);

  type WailsAppBridge = {
    BackendOfflineReason?: () => Promise<string>;
  };

  type WailsRuntimeBridge = {
    EventsOn?: (eventName: string, callback: (payload: unknown) => void) => unknown;
    EventsOff?: (eventName: string, ...additional: string[]) => unknown;
  };

  function appBridge(): WailsAppBridge | null {
    if (typeof window === 'undefined') return null;
    const w = window as Window & { go?: { main?: { App?: WailsAppBridge } } };
    return w.go?.main?.App ?? null;
  }

  function runtimeBridge(): WailsRuntimeBridge | null {
    if (typeof window === 'undefined') return null;
    const w = window as Window & { runtime?: WailsRuntimeBridge };
    return w.runtime ?? null;
  }

  function parseRuntimeBootstrapPayload(raw: unknown): {
    status: 'running' | 'done' | 'error';
    stage: string;
    message: string;
  } | null {
    if (!raw || typeof raw !== 'object') return null;
    const r = raw as Record<string, unknown>;
    const status = String(r.status ?? '').trim();
    const stage = String(r.stage ?? '').trim();
    const message = String(r.message ?? '').trim();
    if (status !== 'running' && status !== 'done' && status !== 'error') return null;
    if (!message) return null;
    return { status, stage, message };
  }

  function extractErrorMessage(err: unknown): string {
    if (err instanceof Error && err.message.trim().length > 0) {
      return err.message;
    }
    return 'Unknown error';
  }

  async function refreshOfflineReason() {
    if (serverOnline) return;
    const bridge = appBridge();
    if (!bridge?.BackendOfflineReason) {
      return;
    }
    try {
      const reason = (await bridge.BackendOfflineReason()).trim();
      if (reason.length > 0) {
        backendStatusTitle = reason;
      }
    } catch (err) {
      backendStatusTitle = `Offline reason fetch failed: ${extractErrorMessage(err)}`;
    }
  }

  async function checkServerHealth() {
    try {
      await api.health();
      serverOnline = true;
      backendStatusTitle = 'Check backend status';
    } catch (err) {
      serverOnline = false;
      backendStatusTitle = `Health check failed: ${extractErrorMessage(err)}`;
    }
  }

  onMount(() => {
    let disposed = false;
    const rt = runtimeBridge();

    const syncHeaderHeight = () => {
      if (!headerEl) return;
      document.documentElement.style.setProperty('--app-header-height', `${headerEl.offsetHeight}px`);
    };

    syncHeaderHeight();
    window.addEventListener('resize', syncHeaderHeight);

    if (rt?.EventsOn) {
      rt.EventsOn('you2midi:runtime-bootstrap', (payload: unknown) => {
        const parsed = parseRuntimeBootstrapPayload(payload);
        if (!parsed) return;
        runtimeBootstrap = parsed;
        backendStatusTitle = parsed.message;
        if (runtimeBannerHideTimer) {
          clearTimeout(runtimeBannerHideTimer);
          runtimeBannerHideTimer = null;
        }
        if (parsed.status === 'done') {
          runtimeBannerHideTimer = setTimeout(() => {
            runtimeBootstrap = null;
          }, 3500);
        }
      });
    }

    void checkServerHealth();
    healthTimer = setInterval(() => {
      if (!disposed) void checkServerHealth();
    }, 5000);

    return () => {
      disposed = true;
      window.removeEventListener('resize', syncHeaderHeight);
      if (healthTimer) clearInterval(healthTimer);
      if (runtimeBannerHideTimer) clearTimeout(runtimeBannerHideTimer);
      if (rt?.EventsOff) rt.EventsOff('you2midi:runtime-bootstrap');
    };
  });
</script>

<svelte:head>
  <title>You2Midi</title>
  <meta name="description" content="Convert YouTube piano videos to MIDI files using AI transcription." />
</svelte:head>

<div class="app-shell">
  <header class="header" bind:this={headerEl}>
    <div class="container header-inner">
      <a href="/" class="logo">
        <span class="logo-text">You<span class="logo-accent">2</span>Midi</span>
      </a>
      <div class="header-right">
        <button
          type="button"
          class="server-badge"
          class:online={serverOnline}
          onclick={checkServerHealth}
          onmouseenter={refreshOfflineReason}
          aria-label="Check backend status"
          title={serverOnline ? 'Check backend status' : backendStatusTitle}
        >
          <span class="server-dot" class:online={serverOnline}>
            <span class="dot"></span>
          </span>
          <span class="server-label">{serverOnline ? 'Backend Online' : 'Backend Offline'}</span>
        </button>
      </div>
    </div>
  </header>

  {#if runtimeBootstrap}
    <div class="runtime-banner" class:done={runtimeBootstrap.status === 'done'} class:error={runtimeBootstrap.status === 'error'}>
      <span class="runtime-dot"></span>
      <div class="runtime-text">
        <strong>
          {#if runtimeBootstrap.status === 'running'}
            Runtime Installing
          {:else if runtimeBootstrap.status === 'done'}
            Runtime Ready
          {:else}
            Runtime Error
          {/if}
        </strong>
        <span>{runtimeBootstrap.message}</span>
      </div>
    </div>
  {/if}

  <main class="main-content">
    {@render children()}
  </main>
</div>

<style>
  :global(:root) {
    --app-header-height: 64px;
  }

  .app-shell {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }

  .header {
    position: sticky;
    top: 0;
    z-index: 100;
    background: rgba(7, 7, 15, 0.8);
    backdrop-filter: blur(20px);
    border-bottom: 1px solid var(--border);
  }
  .header-inner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-top: 1rem;
    padding-bottom: 1rem;
  }

  .logo {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-decoration: none;
    color: var(--text-primary);
    font-size: 1.3rem;
    font-weight: 700;
    letter-spacing: -0.03em;
  }

  .header-right {
    display: flex;
    align-items: center;
  }
  .server-badge {
    display: inline-flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.4rem 0.65rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.03);
    color: var(--text-secondary);
    font: inherit;
    font-size: 0.78rem;
    cursor: pointer;
    transition: border-color 0.2s, background 0.2s, color 0.2s;
  }
  .server-badge:hover {
    border-color: var(--border-glow);
    color: var(--text-primary);
  }
  .server-badge.online {
    border-color: rgba(16, 185, 129, 0.35);
    background: rgba(16, 185, 129, 0.08);
    color: #8ce9bd;
  }

  .server-dot {
    display: inline-flex;
    align-items: center;
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--red);
    transition: background 0.3s;
  }
  .server-dot.online .dot {
    background: var(--green);
    box-shadow: 0 0 6px var(--green);
    animation: pulse-dot 2s ease-in-out infinite;
  }
  .server-label {
    white-space: nowrap;
    line-height: 1;
  }

  @keyframes pulse-dot {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.6; }
  }

  .main-content {
    flex: 1;
    padding: 0 0 4rem;
  }

  .runtime-banner {
    position: sticky;
    top: calc(var(--app-header-height) + 8px);
    z-index: 95;
    margin: 0.6rem auto 0;
    width: min(900px, calc(100% - 2rem));
    display: flex;
    align-items: center;
    gap: 0.7rem;
    padding: 0.7rem 0.9rem;
    border-radius: 12px;
    border: 1px solid rgba(59, 130, 246, 0.35);
    background: rgba(30, 64, 175, 0.2);
    color: #dbeafe;
    backdrop-filter: blur(10px);
  }
  .runtime-banner.done {
    border-color: rgba(16, 185, 129, 0.35);
    background: rgba(16, 185, 129, 0.16);
    color: #d1fae5;
  }
  .runtime-banner.error {
    border-color: rgba(239, 68, 68, 0.4);
    background: rgba(127, 29, 29, 0.28);
    color: #fee2e2;
  }
  .runtime-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #60a5fa;
    box-shadow: 0 0 10px rgba(96, 165, 250, 0.8);
    animation: pulse-dot 1.4s ease-in-out infinite;
    flex: 0 0 auto;
  }
  .runtime-banner.done .runtime-dot {
    background: #34d399;
    box-shadow: 0 0 10px rgba(52, 211, 153, 0.8);
    animation: none;
  }
  .runtime-banner.error .runtime-dot {
    background: #f87171;
    box-shadow: 0 0 10px rgba(248, 113, 113, 0.8);
    animation: none;
  }
  .runtime-text {
    display: flex;
    flex-direction: column;
    line-height: 1.2;
    gap: 0.15rem;
  }
  .runtime-text strong {
    font-size: 0.82rem;
    letter-spacing: 0.02em;
  }
  .runtime-text span {
    font-size: 0.8rem;
    opacity: 0.95;
  }
</style>
