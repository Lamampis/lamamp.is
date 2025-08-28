document.addEventListener("DOMContentLoaded", () => {
  const switcher = document.getElementById("theme-switch");

  switcher.checked = document.documentElement.classList.contains("light");

  switcher.addEventListener("change", () => {
    const old =
      document.getElementById("light-theme") ||
      document.getElementById("dark-theme");
    if (old) old.remove();

    const css = document.createElement("link");
    css.rel = "stylesheet";

    if (switcher.checked) {
      css.id = "light-theme";
      css.href = "/static/style-light.css";
      document.documentElement.classList.add("light");
      document.documentElement.classList.remove("dark");
      localStorage.setItem("theme", "light");
    } else {
      css.id = "dark-theme";
      css.href = "/static/style-dark.css";
      document.documentElement.classList.add("dark");
      document.documentElement.classList.remove("light");
      localStorage.setItem("theme", "dark");
    }
    document.head.appendChild(css);

    // restart svg
    document.querySelectorAll(".icon").forEach((icon) => {
      const clone = icon.cloneNode(true);
      icon.parentNode.replaceChild(clone, icon);
    });
  });
});
