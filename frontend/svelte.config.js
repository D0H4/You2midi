import adapterAuto from '@sveltejs/adapter-auto';
import adapterStatic from '@sveltejs/adapter-static';

const isWailsBuild = process.env.npm_lifecycle_event === 'build:wails';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		adapter: isWailsBuild
			? adapterStatic({
					pages: '../desktop/frontend/dist',
					assets: '../desktop/frontend/dist',
					fallback: 'index.html',
					strict: false
				})
			: adapterAuto(),
		prerender: isWailsBuild ? { entries: ['/'] } : undefined
	}
};

export default config;
