import { useEffect } from "react";
import { ThemeProvider as NextThemesProvider, useTheme } from "next-themes";

export { useTheme };
import { TooltipProvider } from "../ui/tooltip";

// Force-light: visually always light, no matter what next-themes resolves to
// or what external scripts (e.g. Fabric clipper) inject onto <html>.
function ForceLightTheme() {
  useEffect(() => {
    const html = document.documentElement;
    const observer = new MutationObserver(apply);

    function apply() {
      observer.disconnect();
      html.classList.remove("dark");
      html.classList.add("light");
      html.style.colorScheme = "light";
      observer.observe(html, { attributes: true, attributeFilter: ["class"] });
    }

    apply();
    return () => observer.disconnect();
  }, []);

  return null;
}

export function ThemeProvider({
  children,
  ...props
}: React.ComponentProps<typeof NextThemesProvider>) {
  return (
    <NextThemesProvider
      attribute="class"
      defaultTheme="light"
      enableSystem
      disableTransitionOnChange
      {...props}
    >
      <ForceLightTheme />
      <TooltipProvider delay={500}>
        {children}
      </TooltipProvider>
    </NextThemesProvider>
  );
}
