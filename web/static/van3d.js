// A small low-poly diorama: a commuter van on a round road tile with a palm tree,
// turning slowly in warm morning light. Mounted on every [data-van3d] element.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';

const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');

const C = {
  body: 0xf7f5ee,
  stripe: 0x2f8f83,
  glass: 0x24403f,
  tyre: 0x2c3534,
  hub: 0xc9cfc8,
  head: 0xf2b24c,
  tail: 0xe07a6a,
  road: 0x56625f,
  dash: 0xf4efe2,
  grass: 0xc9dcbc,
  sand: 0xe8dcc3,
  trunk: 0xa8825a,
  leaf: 0x5fa47f,
  bush: 0x86b98f,
};

function mat(color, extra = {}) {
  return new THREE.MeshStandardMaterial({ color, roughness: 0.85, metalness: 0, flatShading: true, ...extra });
}

function mesh(geo, material, { x = 0, y = 0, z = 0, cast = true, receive = false } = {}) {
  const m = new THREE.Mesh(geo, material);
  m.position.set(x, y, z);
  m.castShadow = cast;
  m.receiveShadow = receive;
  return m;
}

function buildVan() {
  const van = new THREE.Group();
  const wheels = [];

  // Body: long rounded box with a lower, rounded nose.
  van.add(mesh(new RoundedBoxGeometry(2.3, 1.12, 1.24, 3, 0.2), mat(C.body), { x: -0.2, y: 0.95 }));
  van.add(mesh(new RoundedBoxGeometry(0.75, 0.62, 1.2, 3, 0.18), mat(C.body), { x: 1.15, y: 0.7 }));

  // Windscreen sloping back over the nose, side windows and the sea-glass stripe.
  const screen = mesh(new THREE.BoxGeometry(0.06, 0.5, 1.02), mat(C.glass, { roughness: 0.3 }), { x: 0.93, y: 1.22, cast: false });
  screen.rotation.z = -0.55;
  van.add(screen);
  van.add(mesh(new THREE.BoxGeometry(1.85, 0.36, 1.27), mat(C.glass, { roughness: 0.3 }), { x: -0.22, y: 1.2, cast: false }));
  van.add(mesh(new THREE.BoxGeometry(2.32, 0.11, 1.26), mat(C.stripe), { x: -0.2, y: 0.78, cast: false }));
  van.add(mesh(new THREE.BoxGeometry(0.78, 0.09, 1.22), mat(C.stripe), { x: 1.15, y: 0.66, cast: false }));

  // Lights.
  const head = mat(C.head, { emissive: C.head, emissiveIntensity: 0.6 });
  const tail = mat(C.tail, { emissive: C.tail, emissiveIntensity: 0.4 });
  for (const z of [-0.42, 0.42]) {
    van.add(mesh(new THREE.BoxGeometry(0.05, 0.12, 0.2), head, { x: 1.53, y: 0.78, z, cast: false }));
    van.add(mesh(new THREE.BoxGeometry(0.05, 0.22, 0.12), tail, { x: -1.36, y: 0.95, z, cast: false }));
  }

  // Roof rack.
  for (const x of [-0.85, -0.2, 0.45]) {
    van.add(mesh(new THREE.BoxGeometry(0.08, 0.06, 1.05), mat(C.tyre), { x, y: 1.55 }));
  }

  // Wheels.
  const tyreGeo = new THREE.CylinderGeometry(0.27, 0.27, 0.22, 14);
  tyreGeo.rotateX(Math.PI / 2);
  const hubGeo = new THREE.CylinderGeometry(0.12, 0.12, 0.24, 8);
  hubGeo.rotateX(Math.PI / 2);
  for (const x of [-0.85, 1.0]) {
    for (const z of [-0.55, 0.55]) {
      const w = new THREE.Group();
      w.position.set(x, 0.27, z);
      w.add(mesh(tyreGeo, mat(C.tyre)));
      w.add(mesh(hubGeo, mat(C.hub), { cast: false }));
      wheels.push(w);
      van.add(w);
    }
  }
  return { van, wheels };
}

function buildPalm() {
  const palm = new THREE.Group();
  let y = 0;
  let x = 0;
  for (let i = 0; i < 5; i++) { // a gently leaning trunk
    const seg = mesh(new THREE.CylinderGeometry(0.075 - i * 0.006, 0.09 - i * 0.006, 0.36, 7), mat(C.trunk), { x, y: y + 0.18 });
    seg.rotation.z = -0.08 * i;
    palm.add(seg);
    y += 0.34;
    x += 0.028 * i;
  }
  const leafGeo = new THREE.ConeGeometry(0.16, 1.0, 4);
  leafGeo.translate(0, 0.5, 0);
  for (let i = 0; i < 6; i++) {
    const leaf = mesh(leafGeo, mat(C.leaf), { x, y });
    leaf.rotation.order = 'YZX';
    leaf.rotation.y = (i / 6) * Math.PI * 2;
    leaf.rotation.z = 1.15;
    leaf.scale.set(1, 1, 0.35);
    palm.add(leaf);
  }
  return palm;
}

function buildTile() {
  const tile = new THREE.Group();
  const base = mesh(new THREE.CylinderGeometry(3.05, 2.95, 0.36, 40), [mat(C.sand), mat(C.grass), mat(C.sand)],
    { y: -0.18, cast: false, receive: true });
  tile.add(base);
  tile.add(mesh(new THREE.BoxGeometry(5.3, 0.03, 1.55), mat(C.road), { y: 0.015, cast: false, receive: true }));
  for (let x = -2.2; x <= 2.2; x += 0.75) {
    tile.add(mesh(new THREE.BoxGeometry(0.38, 0.035, 0.07), mat(C.dash), { x, y: 0.02, cast: false, receive: true }));
  }
  const palm = buildPalm();
  palm.position.set(-1.35, 0, -1.7);
  tile.add(palm);
  const bushGeo = new THREE.IcosahedronGeometry(0.28, 0);
  for (const [x, z, s] of [[1.6, -1.65, 1], [1.95, -1.25, 0.7], [-1.9, 1.5, 0.85], [0.6, 2.05, 0.6]]) {
    const b = mesh(bushGeo, mat(C.bush), { x, y: 0.16 * s, z });
    b.scale.setScalar(s);
    tile.add(b);
  }
  return tile;
}

function mount(el) {
  let renderer;
  try {
    renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'low-power' });
  } catch {
    return; // no WebGL: the CSS backdrop stays as a quiet fallback
  }
  renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFSoftShadowMap;
  el.appendChild(renderer.domElement);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(28, 1, 0.1, 100);
  camera.position.set(7.2, 5.2, 8.4);
  camera.lookAt(0, 0.55, 0);

  scene.add(new THREE.HemisphereLight(0xfff4e0, 0x7c958e, 1.9));
  const sun = new THREE.DirectionalLight(0xffe0b0, 2.4);
  sun.position.set(4, 8, 3);
  sun.castShadow = true;
  sun.shadow.mapSize.set(1024, 1024);
  sun.shadow.camera.left = sun.shadow.camera.bottom = -4;
  sun.shadow.camera.right = sun.shadow.camera.top = 4;
  sun.shadow.radius = 4;
  sun.shadow.bias = -0.0008;
  scene.add(sun);

  const world = new THREE.Group();
  world.add(buildTile());
  const { van, wheels } = buildVan();
  van.position.y = 0.03;
  world.add(van);
  world.rotation.y = -0.6;
  scene.add(world);

  function resize() {
    const w = el.clientWidth, h = el.clientHeight;
    if (!w || !h) return;
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    // Keep the whole tile in frame on narrow, tall boxes.
    camera.zoom = Math.min(1, camera.aspect / 1.2);
    camera.updateProjectionMatrix();
    renderer.render(scene, camera);
  }
  new ResizeObserver(resize).observe(el);
  resize();

  // A slow turntable; the pointer adds a slight tilt so the scene responds when touched.
  let tiltX = 0, tiltTarget = 0, spin = 0;
  el.addEventListener('pointermove', (e) => {
    const r = el.getBoundingClientRect();
    tiltTarget = ((e.clientY - r.top) / r.height - 0.5) * 0.18;
    spin = ((e.clientX - r.left) / r.width - 0.5) * 0.6;
  });
  el.addEventListener('pointerleave', () => { tiltTarget = 0; spin = 0; });

  const clock = new THREE.Clock();
  let visible = true;
  function frame() {
    const dt = Math.min(clock.getDelta(), 0.05);
    const t = clock.elapsedTime;
    world.rotation.y += dt * (0.22 + spin);
    tiltX += (tiltTarget - tiltX) * 0.08;
    world.rotation.x = tiltX;
    van.position.y = 0.03 + Math.sin(t * 7) * 0.008; // idling engine
    for (const w of wheels) w.rotation.z -= dt * 0.9;
    renderer.render(scene, camera);
  }
  function update() {
    const run = visible && !document.hidden && !reduceMotion.matches;
    renderer.setAnimationLoop(run ? frame : null);
    if (!run) renderer.render(scene, camera);
  }
  new IntersectionObserver(([e]) => { visible = e.isIntersecting; update(); }).observe(el);
  document.addEventListener('visibilitychange', update);
  reduceMotion.addEventListener('change', update);
  update();
}

document.querySelectorAll('[data-van3d]').forEach(mount);
