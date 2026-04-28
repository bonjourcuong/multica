import { useEffect } from "react";
import { ThemeProvider as NextThemesProvider, useTheme } from "next-themes";

export { useTheme };
import { TooltipProvider } from "../ui/tooltip";

function ThemeClassSync() {
  const { resolvedTheme } = useTheme();

  useEffect(() => {
    if (resolvedTheme !== "light" && resolvedTheme !== "dark") return;

    const html = document.documentElement;
    const stale = resolvedTheme === "dark" ? "light" : "dark";
    const observer = new MutationObserver(apply);

    // Disconnect during our own writes so classList ops do not retrigger us.
    function apply() {
      observer.disconnect();
      html.classList.remove(stale);
      html.classList.add(resolvedTheme);
      html.style.colorScheme = resolvedTheme;
      observer.observe(html, { attributes: true, attributeFilter: ["class"] });
    }

    apply();
    return () => observer.disconnect();
  }, [resolvedTheme]);

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
      <ThemeClassSync />
      <TooltipProvider delay={500}>
        {children}
      </TooltipProvider>
    </NextThemesProvider>
  );
}
