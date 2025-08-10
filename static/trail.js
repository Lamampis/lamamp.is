let colour = "random";
const sparkles = 80; // Max sparkles on screen

let [x, ox, y, oy] = [400, 400, 300, 300];
let [swide, shigh, sleft, sdown] = [800, 600, 0, 0];
const tiny = [];
const star = [];
const starv = [];
const starx = [];
const stary = [];
const tinyx = [];
const tinyy = [];
const tinyv = [];

const colours = [
  "#ff0000",
  "#00ff00",
  "#ffffff",
  "#ff00ff",
  "#ffa500",
  "#ffff00",
  "#00ff00",
  "#ffffff",
  "#ff00ff",
];

const isNetscape = document.layers;
const isIE = document.all;
const isDOM = document.getElementById && !isIE;

// Array of element IDs to exclude
const excludeElements = ["nav", "main"];

function isMouseInExcludedElement(mouseX, mouseY) {
  for (let id of excludeElements) {
    const elem = document.getElementById(id);
    if (elem) {
      const rect = elem.getBoundingClientRect();
      if (
        mouseX >= rect.left + window.pageXOffset &&
        mouseX <= rect.right + window.pageXOffset &&
        mouseY >= rect.top + window.pageYOffset &&
        mouseY <= rect.bottom + window.pageYOffset
      ) {
        return true;
      }
    }
  }
  return false;
}

function Mouse(event) {
  const mouseX = isNetscape || isDOM ? event.pageX : event.clientX + sleft;
  const mouseY = isNetscape || isDOM ? event.pageY : event.clientY + sdown;

  if (!isMouseInExcludedElement(mouseX, mouseY)) {
    y = mouseY;
    x = mouseX;
  }
}

function animate() {
  for (let i = 0; i < sparkles; i++) {
    const temp1 = document.getElementById(`dots${i}`).style;
    const randColour = colours[Math.floor(Math.random() * colours.length)];

    temp1.background = randColour;

    if (i < sparkles - 1) {
      const temp2 = document.getElementById(`dots${i + 1}`).style;
      temp1.top = `${parseInt(temp2.top)}px`;
      temp1.left = `${parseInt(temp2.left)}px`;
    } else {
      temp1.top = `${y}px`;
      temp1.left = `${x}px`;
    }
  }
  setTimeout(animate, 100);
}

window.onload = function () {
  if (document.getElementById) {
    for (let i = 0; i < sparkles; i++) {
      const tinyDiv = createDiv(3, 3);
      tinyDiv.style.visibility = "hidden";
      tinyDiv.style.zIndex = "1";
      document.body.appendChild((tiny[i] = tinyDiv));
      starv[i] = 0;
      tinyv[i] = 0;

      const starDiv = createDiv(5, 5);
      starDiv.style.backgroundColor = "transparent";
      starDiv.style.visibility = "hidden";
      starDiv.style.zIndex = "1";

      const rlef = createDiv(1, 5);
      const rdow = createDiv(5, 1);
      starDiv.appendChild(rlef);
      starDiv.appendChild(rdow);
      rlef.style.top = "2px";
      rlef.style.left = "0px";
      rdow.style.top = "0px";
      rdow.style.left = "2px";

      document.body.appendChild((star[i] = starDiv));
    }
    setDimensions();
    sparkle();
  }
};

function sparkle() {
  if (Math.abs(x - ox) > 1 || Math.abs(y - oy) > 1) {
    ox = x;
    oy = y;
    let sparklesCreated = 0;
    const maxSparklesPerCycle = 1; // Amount

    for (let c = 0; c < sparkles; c++) {
      if (!starv[c] && sparklesCreated < maxSparklesPerCycle) {
        // Random Offset
        const offsetX = x + (Math.random() - 0.5) * 5;
        const offsetY = y + (Math.random() - 0.5) * 5;

        star[c].style.left = `${(starx[c] = offsetX)}px`;
        star[c].style.top = `${(stary[c] = offsetY + 1)}px`;
        star[c].style.clip = "rect(0px, 5px, 5px, 0px)";
        const colourToSet = colour === "random" ? newColour() : colour;
        star[c].childNodes[0].style.backgroundColor = colourToSet;
        star[c].childNodes[1].style.backgroundColor = colourToSet;
        star[c].style.visibility = "visible";
        starv[c] = 80; // Lifetime
        sparklesCreated++;
      }
    }
  }
  for (let c = 0; c < sparkles; c++) {
    if (starv[c]) updateStar(c);
    if (tinyv[c]) updateTiny(c);
  }
  setTimeout(sparkle, 16); // Speed
}

function updateStar(i) {
  if (--starv[i] === 25) star[i].style.clip = "rect(1px, 4px, 4px, 1px)";
  if (starv[i]) {
    stary[i] += 1 + Math.random() * 3;
    starx[i] += ((i % 5) - 2) / 5;
    if (stary[i] < shigh + sdown) {
      star[i].style.top = `${stary[i]}px`;
      star[i].style.left = `${starx[i]}px`;
    } else {
      star[i].style.visibility = "hidden";
      starv[i] = 0;
    }
  } else {
    tinyv[i] = 50;
    tiny[i].style.top = `${(tinyy[i] = stary[i])}px`;
    tiny[i].style.left = `${(tinyx[i] = starx[i])}px`;
    tiny[i].style.width = "2px";
    tiny[i].style.height = "2px";
    tiny[i].style.backgroundColor = star[i].childNodes[0].style.backgroundColor;
    star[i].style.visibility = "hidden";
    tiny[i].style.visibility = "visible";
  }
}

function updateTiny(i) {
  if (--tinyv[i] === 25) {
    tiny[i].style.width = "1px";
    tiny[i].style.height = "1px";
  }
  if (tinyv[i]) {
    tinyy[i] += 1 + Math.random() * 3;
    tinyx[i] += ((i % 5) - 2) / 5;
    if (tinyy[i] < shigh + sdown) {
      tiny[i].style.top = `${tinyy[i]}px`;
      tiny[i].style.left = `${tinyx[i]}px`;
    } else {
      tiny[i].style.visibility = "hidden";
      tinyv[i] = 0;
    }
  } else {
    tiny[i].style.visibility = "hidden";
  }
}

function setDimensions() {
  swide = Math.min(
    document.documentElement.clientWidth || Infinity,
    self.innerWidth || Infinity,
    document.body.clientWidth || Infinity,
  );
  shigh = Math.min(
    document.documentElement.clientHeight || Infinity,
    self.innerHeight || Infinity,
    document.body.clientHeight || Infinity,
  );
}

function createDiv(height, width) {
  const div = document.createElement("div");
  div.style.position = "absolute";
  div.style.height = `${height}px`;
  div.style.width = `${width}px`;
  div.style.overflow = "hidden";
  div.style.padding = "0";
  div.style.margin = "0";
  div.style.border = "none";
  div.style.boxSizing = "content-box";
  return div;
}

function newColour() {
  const c = [
    255,
    Math.floor(Math.random() * 256),
    Math.floor(Math.random() * (256 - Math.random() * 128)),
  ];
  c.sort(() => 0.5 - Math.random());
  return `rgb(${c[0]}, ${c[1]}, ${c[2]})`;
}

function setScroll() {
  sdown =
    window.pageYOffset ||
    document.documentElement.scrollTop ||
    document.body.scrollTop ||
    0;
  sleft =
    window.pageXOffset ||
    document.documentElement.scrollLeft ||
    document.body.scrollLeft ||
    0;
}

document.onmousemove = Mouse;
window.onscroll = setScroll;
window.onresize = setDimensions;

animate();
