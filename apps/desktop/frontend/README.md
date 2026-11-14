# ServerSend desktop frontend

This Vue/TypeScript application is rendered by the Wails desktop client. Go services and generated frontend bindings provide native functionality.

With the existing dependencies installed, `npm run build` checks types with `vue-tsc` and builds production assets with Vite. Do not manually edit `bindings`; regenerate them through the matching Wails CLI when Go services change.

See [Desktop development](../../../docs/desktop-development.md) for full component validation and build prerequisites. UI behaviour and Tailwind light/dark themes will be implemented in the planned desktop feature chunks.
