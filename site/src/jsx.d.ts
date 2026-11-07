// Astro's default JSX.Element is any for framework interoperability. This site
// renders HTML only; give typed ESLint a concrete element type for templates.
import "astro/astro-jsx";

declare global {
  namespace JSX {
    type Element = HTMLElement;
  }
}
