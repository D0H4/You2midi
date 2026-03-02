import type { components } from '$lib/api.generated';

type Job = components['schemas']['Job'];
type ErrorResponse = components['schemas']['ErrorResponse'];
type HealthResponse = components['schemas']['HealthResponse'];
type CreateJobRequest = {
	youtube_url: string;
	device?: 'auto' | 'cpu' | 'cuda';
	start_sec?: number;
};

const DESKTOP_BASE = 'http://127.0.0.1:8080';

function resolveBaseURL(): string {
	if (typeof window === 'undefined') return '';

	const w = window as Window & {
		__YOU2MIDI_API_BASE__?: string;
		go?: { main?: unknown };
	};
	if (typeof w.__YOU2MIDI_API_BASE__ === 'string' && w.__YOU2MIDI_API_BASE__.length > 0) {
		return w.__YOU2MIDI_API_BASE__;
	}

	const protocol = window.location.protocol.toLowerCase();
	const host = window.location.host.toLowerCase();
	const isWailsRuntime = protocol === 'wails:' || host === 'wails.localhost' || !!w.go?.main;
	return isWailsRuntime ? DESKTOP_BASE : '';
}

const BASE = resolveBaseURL();

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(`${BASE}${path}`, {
		headers: { 'Content-Type': 'application/json', ...init?.headers },
		...init,
	});

	if (!res.ok) {
		const err: ErrorResponse = await res.json().catch(() => ({
			error_code: 'NETWORK_ERROR',
			message: `HTTP ${res.status}`,
		}));
		throw new Error(err.message || err.error_code);
	}

	if (res.status === 204 || res.headers.get('Content-Type')?.includes('audio/midi')) {
		return res as unknown as T;
	}
	return res.json() as Promise<T>;
}

export const api = {
	listJobs: (opts?: { limit?: number; before?: string }) => {
		const params = new URLSearchParams();
		if (opts?.limit && opts.limit > 0) {
			params.set('limit', String(opts.limit));
		}
		if (opts?.before) {
			params.set('before', opts.before);
		}
		const query = params.toString();
		return request<Job[]>(query ? `/jobs?${query}` : '/jobs');
	},
	createJob: (body: CreateJobRequest) => request<Job>('/jobs', { method: 'POST', body: JSON.stringify(body) }),
	getJob: (id: string) => request<Job>(`/jobs/${id}`),
	cancelJob: (id: string) => request<Job>(`/jobs/${id}/cancel`, { method: 'POST' }),
	downloadMidi: (id: string): string => `${BASE}/jobs/${id}/midi`,
	health: () => request<HealthResponse>('/health'),
};

export type { Job, CreateJobRequest, ErrorResponse, HealthResponse };
