import { useEffect } from "react";
import { ThemeProvider as NextThemesProvider, useTheme } from "next-themes";

export { useTheme };
import { TooltipProvider } from "../ui/tooltip";

function ThemeClassSync() {
  const { resolvedTheme } = useTheme();
  useEffect(() => {
    if (!resolvedTheme) return;
    const html = document.documentElement;
    const stale = resolvedTheme === "dark" ? "light" : "dark";
    if (html.classList.contains(stale)) {
      html.classList.remove(stale);
    }
    if (!html.classList.contains(resolvedTheme)) {
      html.classList.add(resolvedTheme);
    }
    html.style.colorScheme = resolvedTheme;
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
