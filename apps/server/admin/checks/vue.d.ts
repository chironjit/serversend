// Oxc's TypeScript checker cannot inspect Vue SFCs. Supply the module boundary
// for it; the separate vue-tsc pass resolves the actual component/templates.
declare module '*.vue' {
  import type { DefineComponent } from 'vue';
  const component: DefineComponent;
  export default component;
}
