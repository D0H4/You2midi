<script lang="ts">
  import '../app.css';
  import { api } from '$lib/api';
  import { onMount } from 'svelte';

  let { children } = $props();
  let serverOnline = $state(false);
  let backendStatusTitle = $state('Check backend status');
  let headerEl: HTMLElement | null = null;
  let healthTimer: ReturnType<typeof setInterval> | null = null;

  type WailsAppBridge = {
    BackendOfflineReason?: () => Promise<string>;
  };

  function appBridge(): WailsAppBridge | null {
    if (typeof window === 'undefined') return null;
    const w = window as Window & { go?: { main?: { App?: WailsAppBridge } } };
    return w.go?.main?.App ?? null;
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

    const syncHeaderHeight = () => {
      if (!headerEl) return;
      document.documentElement.style.setProperty('--app-header-height', `${headerEl.offsetHeight}px`);
    };

    syncHeaderHeight();
    window.addEventListener('resize', syncHeaderHeight);

    void checkServerHealth();
    healthTimer = setInterval(() => {
      if (!disposed) void checkServerHealth();
    }, 5000);

    return () => {
      disposed = true;
      window.removeEventListener('resize', syncHeaderHeight);
      if (healthTimer) clearInterval(healthTimer);
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
</style>
