// Shared production renderer and admin preview. No textures or pointer listeners.
export const studies = [
  {
    name: "Mercury",
    description:
      "Liquid satin. Broad sculptural folds, fine ridges, and a wandering specular highlight.",
    colors: ["#788b9a", "#c4b8a6", "#a5b1c0"],
    presence: 65,
    scale: 95,
    detail: 42,
    speed: 22,
    grain: 10,
    protect: 65,
  },
  {
    name: "Overprint",
    description:
      "Color suspended in transparent washes. Soft density gradients dissolve into paper; intersections release a trace of light.",
    colors: ["#7c9cba", "#b28fae", "#d4ae91"],
    presence: 88,
    scale: 100,
    detail: 48,
    speed: 24,
    grain: 12,
    protect: 55,
  },
  {
    name: "Nacre",
    description:
      "A thin optical film. Pearlescent color follows the folds; a broad reflection slowly reveals its surface.",
    colors: ["#86a9b2", "#b9a0b7", "#d8bd94"],
    presence: 85,
    scale: 100,
    detail: 48,
    speed: 24,
    grain: 10,
    protect: 52,
  },
  {
    name: "Lensing",
    description:
      "Optical glass over a drifting color field. Fine dispersion, a polished rim, and a long reflection moving across the surface.",
    colors: ["#64859f", "#be927a", "#b7a4bd"],
    presence: 60,
    scale: 100,
    detail: 42,
    speed: 18,
    grain: 6,
    protect: 82,
  },
  {
    name: "Filament",
    description:
      "Colored silk, lit along the fiber. Petrol, copper and violet strands turn through two drifting bands of reflected light.",
    colors: ["#146e86", "#c25a39", "#7557b7"],
    presence: 58,
    scale: 100,
    detail: 48,
    speed: 14,
    grain: 6,
    protect: 84,
  },
  {
    name: "Monolith",
    description:
      "A porcelain knot under studio light. A continuous surface turns slowly through long, narrow reflections.",
    colors: ["#677b90", "#bdc1c7", "#b9a08c"],
    presence: 88,
    scale: 100,
    detail: 45,
    speed: 22,
    grain: 10,
    protect: 45,
  },
];
export const palettes = [
  { id: "signature", name: "Signature", light: null, dark: null },
  {
    id: "mineral",
    name: "Mineral",
    light: ["#426f83", "#ba806b", "#9286ac"],
    dark: ["#6eabb6", "#d3a085", "#aca0ca"],
  },
  {
    id: "tidal",
    name: "Tidal",
    light: ["#416985", "#609d9d", "#a2b1bd"],
    dark: ["#779cc6", "#77bdb5", "#b5c1ce"],
  },
  {
    id: "ember",
    name: "Ember",
    light: ["#a36450", "#c19b65", "#73848d"],
    dark: ["#ce8f74", "#d2b988", "#94a9b3"],
  },
  {
    id: "orchid",
    name: "Orchid",
    light: ["#80638e", "#ae8c9c", "#b4a28b"],
    dark: ["#af91c8", "#d5a5b4", "#c6b79e"],
  },
  {
    id: "verdigris",
    name: "Verdigris",
    light: ["#507d76", "#9c8e69", "#b98972"],
    dark: ["#85b5a2", "#bab18b", "#cfaa8b"],
  },
  {
    id: "graphite",
    name: "Graphite",
    light: ["#65747f", "#8d9296", "#b2aaa0"],
    dark: ["#97a8b8", "#b3bac1", "#bfb8ac"],
  },
];

const vertex = `
attribute vec2 a_position;
void main(){gl_Position=vec4(a_position,0.,1.);}
`;
const fragment = `
precision highp float;
uniform vec2 u_resolution,u_reading;
uniform float u_time,u_grainTime,u_mode,u_presence,u_scale,u_detail,u_grain,u_protect,u_dark,u_seed,u_filaments,u_lines,u_lineSize,u_fiberWidth,u_lineSpacing,u_fiberSpacing;
uniform vec3 u_color1,u_color2,u_color3;
#define PI 3.14159265359
float hash(vec2 p){vec3 p3=fract(vec3(p.xyx)*.1031);p3+=dot(p3,p3.yzx+33.33);return fract((p3.x+p3.y)*p3.z);}
mat2 rot(float a){return mat2(cos(a),-sin(a),sin(a),cos(a));}
float surface(vec2 p,float t){
 vec2 q=rot(.65)*p;
 float flow=q.x+.4*sin(q.y*1.5+t*.18)+.13*sin(q.y*3.3-t*.11);
 return .20*sin(flow*5.)+.048*sin(flow*10.+q.y*.8)+.025*sin(flow*(18.+u_detail*16.));
}
float film(vec2 q,float t){
 q.x+=.22*sin(q.y*1.8+t*.08);
 return .21*sin(q.x*3.4+q.y*.7)+.09*sin(q.x*6.2-q.y*1.3+t*.12)+.025*sin(q.y*5.+q.x*3.);
}
float sculpture(vec3 p,float t){
 p.xz=rot(.35+t*.06)*p.xz;p.xy=rot(-.42)*p.xy;
 p.xz=rot(p.y*(.55+u_detail*.65))*p.xz;
 vec2 q=vec2(length(p.xy)-.57,p.z);
 return length(q)-(.18+.045*sin(atan(p.y,p.x)*3.+t*.1));
}
// Shared projected color field; parameters that do not vary by veil are hoisted.
vec3 opticalField(vec2 q,float t,float blur){
 q=rot(-.32)*q;vec3 density=vec3(0.);
 float frequency=u_lines*2.*PI/u_lineSpacing;
 float exponent=u_lineSpacing*u_lineSpacing/max(u_lineSize,.25);
 float average=1./sqrt(1.+3.*exponent);
 float resolved=exp(-blur*blur*frequency*frequency*max(1.,exponent));
 float fade=exp(-pow(abs(q.x)/1.1,6.));
 for(int i=0;i<3;i++){
  float f=float(i);float center=(f-1.)*.18+.075*sin(q.x*3.+f*1.5-t*.14);
  float d=q.y-center;if(abs(d)>.36)continue;float envelope=exp(-pow(d/.095,2.));
  float screen=pow(max(.00001,.5+.5*sin(d*frequency+f)),exponent);
  screen=mix(average,screen,resolved);
  vec3 pigment=i==0?u_color1:(i==1?u_color2:u_color3);
  density+=mix(1.-pigment,pigment,u_dark)*envelope*(.25+.55*screen)*fade;
 }
 return exp(-density);
}
// A sphere's refracted ray stays in its radial plane. Trace RGB together;
// share the entry geometry and evaluate only each wavelength's own color channel.
vec3 lensRadii(float radius,float radial,float height,vec3 ior){
 float nx=radial/radius,nz=height/radius;
 vec3 eta=1./ior;
 vec3 bend=eta*nz-sqrt(max(vec3(0.),1.-eta*eta*(1.-nz*nz)));
 vec3 dx=bend*nx,dz=-eta+bend*nz;
 vec3 chord=2.*(radial*dx+height*dz);
 vec3 ex=radial-dx*chord,ez=height-dz*chord;
 vec3 cosine=-(ex*dx+ez*dz)/radius;
 vec3 exitBend=ior*cosine+sqrt(max(vec3(0.),1.-ior*ior*(1.-cosine*cosine)));
 vec3 ox=ior*dx+exitBend*ex/radius;
 vec3 oz=ior*dz+exitBend*ez/radius;
 return ex+ox*((-.72-ez)/min(oz,vec3(-.001)));
}
vec3 opticalChannels(vec3 x,vec3 y,float t,float blur){
 vec3 qx=cos(-.32)*x+sin(-.32)*y;
 vec3 qy=-sin(-.32)*x+cos(-.32)*y;
 float frequency=u_lines*2.*PI/u_lineSpacing;
 float exponent=u_lineSpacing*u_lineSpacing/max(u_lineSize,.25);
 float average=1./sqrt(1.+3.*exponent);
 float resolved=exp(-blur*blur*frequency*frequency*max(1.,exponent));
 vec3 fade=exp(-pow(abs(qx)/1.1,vec3(6.))),density=vec3(0.);
 for(int i=0;i<3;i++){
  float f=float(i);vec3 center=(f-1.)*.18+.075*sin(qx*3.+f*1.5-t*.14);
  vec3 d=qy-center;vec3 ad=abs(d);if(min(ad.x,min(ad.y,ad.z))>.36)continue;vec3 envelope=exp(-pow(d/.095,vec3(2.)));
  vec3 screen=pow(max(vec3(.00001),.5+.5*sin(d*frequency+f)),vec3(exponent));
  screen=mix(vec3(average),screen,resolved);
  vec3 pigment=i==0?u_color1:(i==1?u_color2:u_color3);
  density+=mix(1.-pigment,pigment,u_dark)*envelope*(.25+.55*screen)*fade;
 }
 return exp(-density);
}
void main(){
 vec2 uv=gl_FragCoord.xy/u_resolution;
 vec2 p=(gl_FragCoord.xy-.5*u_resolution)/u_resolution.y;
 p=p*u_scale+vec2(.31,-.16);
 // Protect the reading column independently of the composition and scroll position.
 float integrated=smoothstep(0.,.65,u_protect);
 float portrait=1.-smoothstep(.65,1.3,u_resolution.x/u_resolution.y);
 // On phones, optical dimensions follow screen width rather than the tall viewport.
 float lensUnit=mix(u_resolution.y,min(u_resolution.x,u_resolution.y),portrait);
 float lensPixelScale=u_resolution.y/lensUnit;
 if(u_mode>2.5&&u_mode<4.5){
  vec2 anchor=u_mode<3.5?mix(vec2(.72,.55),vec2(.66,.53),portrait):vec2(.52,.52);
  vec2 origin=u_mode<3.5?vec2(.23,-.08):vec2(.22,-.07);
  vec2 layout=(uv-anchor)*u_resolution/u_resolution.y*u_scale*(u_mode<3.5?lensPixelScale:1.)+origin;
  p=layout;
 }
 float t=u_time;vec3 paper=mix(vec3(1.),vec3(.051,.055,.063),u_dark);
 vec3 art=paper;float shine=0.;
 // Distances are measured in viewport-height units; AA follows output resolution.
 float aa=1.35*u_scale/u_resolution.y;
 if(u_mode>2.5&&u_mode<3.5)aa*=lensPixelScale;
 if(u_mode<.5){
  float e=.006;float h=surface(p,t);
  float hx=surface(p+vec2(e,0),t),hy=surface(p+vec2(0,e),t);
  vec3 normal=normalize(vec3((h-hx)/e,(h-hy)/e,1.));
  vec3 lamp=normalize(vec3(-.55,.8,1.1));
  float diffuse=dot(normal,lamp)*.5+.5;
  float spec=pow(max(dot(normal,normalize(lamp+vec3(0,0,1))),0.),48.);
   float fres=pow(1.-normal.z,2.);
   float ribbon=.5+.5*sin(h*10.+normal.x*1.8+p.y*.6);
   vec3 metal=mix(u_color1,u_color2,ribbon);
   art=mix(paper,metal,.27+.62*pow(1.-diffuse,1.3));
   art=mix(art,u_color3,fres*.45);shine=spec*.95;

 }else if(u_mode<1.5){
  // Density fields, not filled silhouettes. Screen light through a colored wash;
  // a bounded difference term releases complementary color at intersections.
  // Enter from the lateral edges: a shallow diagonal crossing, with an open crown.
  vec2 centered=(uv-vec2(.5,.48))*u_resolution/u_resolution.y*u_scale;
  vec2 q=rot(1.34)*centered*.82;
  vec3 ink=vec3(0.),light=vec3(0.),dye=vec3(0.);float last=0.;
  for(int i=0;i<3;i++){
   float f=float(i);vec2 v=rot((f-1.)*.28)*q;
   float bend=.17*sin(v.y*2.2+t*.12+f*1.7)+.10*v.y*v.y;
   float d=v.x-(f-1.)*.23-bend;
   float width=.10+.065*(.5+.5*sin(v.y*2.-f+t*.08));
   float veil=exp(-pow(abs(d)/width,1.65));
   veil*=exp(-pow(abs(v.y+.1*f)/1.45,4.));
   // Ease the upper edge to paper instead of clipping a vertical wash into the header.
   veil*=1.-smoothstep(.66,.98,uv.y);
   float wisp=.5+.5*sin(v.y*3.1+d*5.+f*2.);
   veil*=.25+.65*wisp;
   vec3 pigment=i==0?u_color1:(i==1?u_color2:u_color3);
   ink+=(vec3(1.)-pigment)*veil*.92;
   dye+=pigment*veil*.40;
   vec3 tint=mix(pigment,pigment.brg,.4);
   float overlap=veil*last;
   light=1.-(1.-light)*(1.-tint*overlap*.48);
   ink+=abs(pigment-pigment.gbr)*overlap*.24;
   float seam=exp(-pow((d+width*.32)/(.005+u_detail*.007),2.));
   light+=tint*seam*veil*.12;
   last=veil;
  }
  vec3 wash=exp(-ink);
  vec3 screened=1.-(1.-wash)*(1.-light);
  art=mix(screened,paper+dye*.60+light*.25,u_dark);
 }else if(u_mode<2.5){
  // NACRE: interference tracks optical film thickness, lit by a broad softbox.
  vec2 q=rot(-.4)*(p-vec2(.15,0.));
  float h=film(q,t);float e=.003;
  vec2 slope=vec2(film(q+vec2(e,0),t)-h,film(q+vec2(0,e),t)-h)/e;
  vec3 n=normalize(vec3(-slope*2.6,1.));
  float grazing=pow(1.-n.z,1.5);
  float thickness=h*(2.8+u_detail*3.)+n.x*.32;
  vec3 interference=.5+.5*cos(thickness*6.283+vec3(.3,2.1,4.2));
  vec3 pigment=mix(u_color1,u_color2,interference.r);
  pigment=mix(pigment,u_color3,interference.b*.65);
  float ridge=pow(max(0.,dot(n,normalize(vec3(-.65,.3,1.)))),22.);
  float veil=exp(-pow((q.x+.18*sin(q.y*2.))/ .64,4.));
  art=mix(paper,pigment,veil*(.27+grazing*.7));
  shine=ridge*veil*.48;
  art=mix(art,u_color2,exp(-pow((h-.12)*33.,2.))*veil*.08);
 }else if(u_mode<3.5){
  // LENSING: two Snell interfaces, then project transmitted rays onto the source.
  // Each color channel has its own index of refraction (subtle dispersion).
  vec2 center=vec2(.23+.035*sin(t*.19),-.08+.025*cos(t*.17));
  vec2 q=p-center;float radius=.325*.85;float r=length(q);
  float blur=u_scale/lensUnit;
  float lightTurn=.065*sin(t*.13);
  // The fully covered interior does not need an unrefracted background sample.
  if(r>radius-aa*2.){
   vec3 background=opticalField(p-vec2(.22,-.12),t,blur);
   art=mix(background,paper+(1.-background)*.55,u_dark);
  }
  float mask=1.-smoothstep(radius-aa*2.,radius,r);
  if(r<radius){
   float dispersion=.002+u_detail*.009;
   float height=sqrt(max(0.,radius*radius-r*r));
   vec3 projected=lensRadii(radius,r,height,vec3(1.455-dispersion,1.455,1.455+dispersion));
   vec2 direction=q/max(r,.000001);
   vec2 offset=center-vec2(.22,-.12);
   float edgeBlur=blur*(1.+pow(r/radius,12.)*6.);
   vec3 transmitted=opticalChannels(direction.x*projected+offset.x,direction.y*projected+offset.y,t,edgeBlur);
   vec3 normal=vec3(q,height)/radius;
   vec3 reflection=reflect(vec3(0.,0.,-1.),normal);
   reflection.xy=rot(lightTurn)*reflection.xy;
   float fresnel=.03435+.96565*pow(1.-normal.z,5.);
   // Small wavelength-dependent absorption is strongest through the thick center.
   vec3 tint=mix(u_color1,u_color3,.35);
   transmitted*=exp(-(1.-tint)*normal.z*.018*(1.-u_dark));
   float box=exp(-pow((reflection.x+.48)*3.5,2.)-pow((reflection.y-.5)*2.,6.));
   float strip=exp(-pow((reflection.x-.6)*15.,2.))*smoothstep(-.7,.5,reflection.y);
   vec3 lens=mix(transmitted,paper+(1.-transmitted)*.65,u_dark);
   lens=mix(lens,mix(u_color1,paper,.58),fresnel*.40);
   lens+=mix(vec3(1.),vec3(.35),u_dark)*(box*.19+strip*.10)*(fresnel+.12);
   art=mix(art,lens,mask);
  }
  // A narrow refracted crescent anchors the object to the surrounding light field.
  float caustic=exp(-pow((length(q-rot(lightTurn)*vec2(.017,-.012))-(radius+.013))/.007,2.));
  float arc=pow(max(0.,dot(normalize(q+vec2(.00001)),rot(lightTurn)*normalize(vec2(.7,-.5)))),6.);
  art=mix(art,mix(u_color2,u_color3,.25),caustic*arc*.13);
  float spill=exp(-pow((length(q-rot(lightTurn)*vec2(.026,-.019))-(radius+.022))/.018,2.))*arc;
  art=mix(art,mix(paper,u_color2,.28),spill*.055);
 }else if(u_mode<4.5){
  // FILAMENT: colored fibers with tangent-dependent primary and tinted highlights.
  float sway=.11*sin(t*.14);
  vec2 drift=vec2(.045*sin(t*.11),.07*sin(t*.09));
  vec2 field=rot(-.23+sway)*(p-vec2(.22,-.07)-drift)*.60;
  float breathing=1.+.12*sin(t*.17);
  float bendAmplitude=.13+.035*sin(t*.18);
  float fiberAA=aa*.60;
  vec3 absorption=vec3(0.),emission=vec3(0.),fiberColor=vec3(0.);
  vec3 previousColor=vec3(0.),previousInk=vec3(0.);
  float previousCoverage=0.,previousDepth=0.,previousSlope=0.;
  for(int layer=0;layer<2;layer++){
   float l=float(layer);vec2 q=rot((l-.5)*.27)*field;
   vec3 layerInk=vec3(0.),layerColor=vec3(0.),layerLight=vec3(0.);
   float coverage=0.,depthSum=0.,slopeSum=0.;
   float motion=t*.16+l*2.2;
   float spread=(.105+.08*(.5+.5*sin(q.x*2.1+motion)))*u_fiberSpacing*breathing;
   float center=bendAmplitude*sin(q.x*2.5+motion)+(l-.5)*.12;
   float width=(.0003+u_detail*.0004)*.60*u_fiberWidth;
   // Filter the physical strand width across the pixel footprint (unit-area kernel).
   // This preserves colored coverage when individual fibers become subpixel.
   float featherWidth=width+fiberAA*1.25;
   float lengthFade=exp(-pow(abs(q.x)/1.12,6.));
   if(abs(q.y-center)>spread+.036+featherWidth*2.||lengthFade<.0001)continue;
   float baseSlope=bendAmplitude*2.5*cos(q.x*2.5+motion);
   float spreadSlope=.084*cos(q.x*2.1+motion)*u_fiberSpacing*breathing;
   // The displacement is bounded by .035, so only nearby strand indices can cover us.
   float count=floor(u_filaments*.5+.5);
   float nearest=.5+(q.y-center)/(2.*spread);
   float reach=(.035+featherWidth*1.5)/(2.*spread);
   float first=max(0.,floor((nearest-reach)*count-.5));
   float last=min(count-1.,ceil((nearest+reach)*count-.5));
   for(int i=0;i<160;i++){
    float index=first+float(i);if(index>last)break;
    float f=(index+.5)/count;float v=f*2.-1.;
    float theta=q.x*2.15+motion+f*.65;
    float y=center+v*spread+.035*sin(f*PI)*cos(theta*1.6);
    float slope=baseSlope+v*spreadSlope-.1204*sin(f*PI)*sin(theta*1.6);
    float distance=abs(q.y-y)/sqrt(1.+slope*slope);
    if(distance>=featherWidth)continue;
    vec2 screenNormal=rot(.23-sway-(l-.5)*.27)*normalize(vec2(-slope,1.));
    float footprint=(fiberAA/1.35)*(abs(screenNormal.x)+abs(screenNormal.y))*1.1;
    float signedDistance=(q.y-y)/sqrt(1.+slope*slope);
    float strand=smoothstep(-footprint,footprint,signedDistance+width)
                -smoothstep(-footprint,footprint,signedDistance-width);
    float fade=lengthFade*pow(sin(f*PI),.55);
    vec3 tangent=normalize(vec3(1.,slope,.32*cos(theta+f*2.)));
    float tl=dot(tangent,normalize(vec3(.25,.8,.55)));
    float primary=pow(max(0.,1.-tl*tl),16.);
    float secondary=pow(max(0.,1.-pow(clamp(tl+.18,-1.,1.),2.)),9.);
    float hue=smoothstep(.08,.92,f);
    vec3 pigment=layer==0?mix(u_color1,u_color3,hue*.65):mix(u_color3,u_color2,hue);
    pigment*=.92+.08*sin(theta*.7+f*2.);
    float density=strand*fade*(.72+.3*sin(f*PI));
    // Color belongs to each fiber, rather than to a blurred field behind the lines.
    layerInk+=(1.-pigment)*density*(1.-primary*.45);
    layerColor+=pigment*density;
    layerLight+=mix(pigment,vec3(1.),.35)*primary*density*.10;
    layerLight+=pigment*secondary*density*.055;
    coverage+=density;
    depthSum+=sin(theta+f*1.4)*density;
    slopeSum+=(slope+(l-.5)*.27)*density;
   }
   vec3 pigment=layerColor/max(coverage,.0001);
   float depth=depthSum/max(coverage,.0001);
   float direction=slopeSum/max(coverage,.0001);
   float crossing=(1.-exp(-coverage*2.))*(1.-exp(-previousCoverage*2.));
   float front=smoothstep(-.18,.18,depth-previousDepth);
   float angle=clamp(abs(direction-previousSlope)*1.7,0.,1.);
   // At actual strand crossings, trade over/under light and mix transmitted color.
   vec3 transmitted=sqrt(max(pigment*previousColor,vec3(0.)));
   vec3 interference=mix(transmitted,1.-abs(pigment-previousColor),.22+.25*angle);
   absorption-=previousInk*crossing*front*.32;
   absorption+=layerInk*(1.-crossing*(1.-front)*.32);
   absorption+=(1.-transmitted)*crossing*.10;
   fiberColor+=layerColor;
   emission+=layerLight+interference*crossing*(.13+.14*angle);
   previousInk=layerInk;previousColor=pigment;
   previousCoverage=coverage;previousDepth=depth;previousSlope=direction;
  }
  vec3 silk=exp(-absorption);
  art=mix(1.-(1.-silk)*(1.-emission),paper+fiberColor*.23+emission*.38,u_dark);
 }else{
  // MONOLITH: ray-marched porcelain knot with a studio reflection environment.
  vec2 q=p-vec2(.27,-.10);vec3 ro=vec3(q*2.25,2.8),rd=vec3(0.,0.,-1.);
  float travel=0.;vec3 pos;bool hit=false;
  for(int i=0;i<56;i++){pos=ro+rd*travel;float d=sculpture(pos,t);if(d<.0015){hit=true;break;}travel+=d*.78;if(travel>5.)break;}
  if(hit){
   vec2 e=vec2(.002,0.);vec3 n=normalize(vec3(sculpture(pos+e.xyy,t)-sculpture(pos-e.xyy,t),sculpture(pos+e.yxy,t)-sculpture(pos-e.yxy,t),sculpture(pos+e.yyx,t)-sculpture(pos-e.yyx,t)));
   vec3 reflected=reflect(rd,n);
   float diffuse=dot(n,normalize(vec3(-.5,.8,1.)))*.5+.5;
   float softbox=exp(-pow((reflected.x+.4)*4.,2.)-pow((reflected.y-.55)*1.5,6.));
   float strip=exp(-pow((reflected.x-.55)*18.,2.))*.45;
   float fresnel=pow(1.-max(n.z,0.),3.);
   vec3 body=mix(u_color1,u_color2,smoothstep(.2,.85,diffuse));
   body=mix(body,u_color3,fresnel*.65);
   float ao=clamp(sculpture(pos+n*.12,t)/.12,.35,1.);
   art=mix(paper,body,(.4+.5*(1.-diffuse))*ao+.18*(1.-ao));
   shine=softbox*.82+strip;
  }
 }
 if(u_dark>.5){art=mix(paper,art,u_mode>2.5&&u_mode<4.5?.72:.54);art+=shine*vec3(.16,.18,.2);}else{art=mix(art,vec3(1.),clamp(shine,0.,.95));}
 // Preserve a quiet reading field; let the material emerge around and below it.
 vec2 reading=(uv-vec2(.47,.78))/vec2(.56,.37);
 float shield=exp(-dot(reading,reading)*1.4)*u_protect;
 float edge=.76+.24*smoothstep(.15,.8,length(uv-vec2(.5)));
 float presence=u_presence;
 if(u_mode>2.5&&u_mode<4.5){
  // Preserve chroma at low contrast; avoid making dark-mode fibers look inverted.
  float luminance=dot(art,vec3(.2126,.7152,.0722));
  art=mix(vec3(luminance),art,mix(.83,.92,u_dark));
  float column=smoothstep(u_reading.x-.09,u_reading.x+.055,uv.x)
              *(1.-smoothstep(u_reading.y-.055,u_reading.y+.09,uv.x));
  shield=mix(shield,column*u_protect,integrated);
  presence*=mix(1.,1.12,u_dark)*mix(1.,.85,portrait*integrated);
 }
 vec3 col=mix(paper,art,presence*edge*(1.-shield));
 // A separate 24 Hz clock animates grain even when the material moves imperceptibly.
 float grainFrame=mod(floor(u_grainTime*24.),65536.);
 vec2 grainOffset=vec2(mod(grainFrame,251.)*17.17,mod(grainFrame,257.)*47.77)+u_seed*31.;
 col+=(hash(gl_FragCoord.xy+grainOffset)-.5)*u_grain*.035;
 gl_FragColor=vec4(col,1.);
}
`;
function rgb(hex) {
  return hex
    .slice(1)
    .match(/../g)
    .map((v) => parseInt(v, 16) / 255);
}
function createRenderer(canvas, vertex, fragment) {
  const gl = canvas.getContext("webgl", {
    alpha: false,
    antialias: false,
    powerPreference: "low-power",
    preserveDrawingBuffer: false,
  });
  if (!gl) throw Error("WebGL is unavailable");
  const program = gl.createProgram();
  const shaders = [];
  for (const [type, source] of [
    [gl.VERTEX_SHADER, vertex],
    [gl.FRAGMENT_SHADER, fragment],
  ]) {
    const s = gl.createShader(type);
    gl.shaderSource(s, source);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS))
      throw Error(gl.getShaderInfoLog(s));
    gl.attachShader(program, s);
    shaders.push(s);
  }
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS))
    throw Error(gl.getProgramInfoLog(program));
  gl.useProgram(program);
  shaders.forEach((s) => gl.deleteShader(s));
  const buffer = gl.createBuffer();
  gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
  gl.bufferData(
    gl.ARRAY_BUFFER,
    new Float32Array([-1, -1, 3, -1, -1, 3]),
    gl.STATIC_DRAW,
  );
  const a = gl.getAttribLocation(program, "a_position");
  gl.enableVertexAttribArray(a);
  gl.vertexAttribPointer(a, 2, gl.FLOAT, false, 0, 0);
  const u = {};
  [
    "resolution",
    "reading",
    "filaments",
    "lines",
    "lineSize",
    "fiberWidth",
    "lineSpacing",
    "fiberSpacing",
    "grainTime",
    "time",
    "mode",
    "presence",
    "scale",
    "detail",
    "grain",
    "protect",
    "dark",
    "seed",
    "color1",
    "color2",
    "color3",
  ].forEach((k) => (u[k] = gl.getUniformLocation(program, "u_" + k)));
  let colorKey = "",
    converted = [];
  return {
    gl,
    draw(s, t, d, protect, reading = [0.18, 0.82], grainTime = t) {
      gl.viewport(0, 0, canvas.width, canvas.height);
      gl.uniform2f(u.resolution, canvas.width, canvas.height);
      gl.uniform2fv(u.reading, reading);
      gl.uniform1f(u.time, t);
      gl.uniform1f(u.grainTime, grainTime);
      gl.uniform1f(u.mode, s.mode);
      gl.uniform1f(u.presence, s.presence / 100);
      gl.uniform1f(u.scale, s.scale / 100);
      gl.uniform1f(u.detail, s.detail / 100);
      gl.uniform1f(u.grain, s.grain / 100);
      gl.uniform1f(u.protect, protect ?? s.protect / 100);
      gl.uniform1f(u.dark, d);
      gl.uniform1f(u.seed, 1);
      gl.uniform1f(u.filaments, s.filaments ?? 80);
      gl.uniform1f(u.lines, s.lines ?? 28);
      gl.uniform1f(u.lineSize, (s.lineSize ?? 100) / 100);
      gl.uniform1f(u.fiberWidth, (s.fiberWidth ?? 100) / 100);
      gl.uniform1f(u.lineSpacing, (s.lineSpacing ?? 100) / 100);
      gl.uniform1f(u.fiberSpacing, (s.fiberSpacing ?? 100) / 100);
      const colors = d ? s.darkColors || s.colors : s.colors;
      const key = colors.join("");
      if (key !== colorKey) {
        colorKey = key;
        converted = colors.map(rgb);
      }
      for (let i = 0; i < 3; i++)
        gl.uniform3fv(u["color" + (i + 1)], converted[i]);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    },
    dispose(lose = false) {
      gl.deleteBuffer(buffer);
      gl.deleteProgram(program);
      if (lose) gl.getExtension("WEBGL_lose_context")?.loseContext();
    },
  };
}

export function preset(mode = 3, dark = false) {
  const s = studies[mode];
  return {
    mode,
    palette: "signature",
    colors: [...s.colors],
    presence: s.presence,
    scale: s.scale,
    detail: s.detail,
    speed: s.speed,
    protect: s.protect,
    grain: s.grain,
    filaments: 80,
    lines: 28,
    lineSize: 100,
    fiberWidth: 100,
    lineSpacing: 100,
    fiberSpacing: 100,
    quality: 1,
  };
}
export function defaults() {
  return {
    enabled: false,
    profiles: Object.fromEntries(
      ["desktopLight", "desktopDark", "mobileLight", "mobileDark"].map((k) => [
        k,
        preset(),
      ]),
    ),
    presets: [],
  };
}
let grainTexture = "";
function noiseTexture(doc) {
  if (grainTexture) return grainTexture;
  const tile = doc.createElement("canvas");
  tile.width = tile.height = 128;
  const ctx = tile.getContext("2d");
  const pixels = ctx.createImageData(128, 128);
  for (let i = 0; i < pixels.data.length; i += 4) {
    const v = Math.random() * 255;
    pixels.data[i] = pixels.data[i + 1] = pixels.data[i + 2] = v;
    pixels.data[i + 3] = 255;
  }
  ctx.putImageData(pixels, 0, 0);
  return (grainTexture = tile.toDataURL());
}
export function mountBackground(doc, getProfile) {
  const win = doc.defaultView,
    canvas = doc.createElement("canvas");
  canvas.id = "site-background";
  canvas.setAttribute("aria-hidden", "true");
  doc.body.prepend(canvas);
  const grain = doc.createElement("div");
  grain.id = "site-background-grain";
  grain.setAttribute("aria-hidden", "true");
  grain.style.backgroundImage = "url(" + noiseTexture(doc) + ")";
  canvas.after(grain);
  const reduced = win.matchMedia("(prefers-reduced-motion: reduce)"),
    scheme = win.matchMedia("(prefers-color-scheme: dark)"),
    mobile = win.matchMedia("(max-width: 768px)");
  let renderer = null,
    mode = -1,
    raf = 0,
    timer = 0,
    last = 0,
    time = 0,
    dirty = true,
    lost = false,
    failed = false,
    destroyed = false,
    settings,
    renderSettings,
    reading = [0.18, 0.82],
    dark = false;
  function wake() {
    if (destroyed || doc.hidden || lost || raf || timer) return;
    raf = win.requestAnimationFrame(tick);
  }
  function update() {
    dark =
      doc.documentElement.dataset.theme === "dark" ||
      (!["light", "dark"].includes(doc.documentElement.dataset.theme) &&
        scheme.matches);
    settings = getProfile(mobile.matches, dark);
    if (failed && settings?.mode === mode) {
      canvas.hidden = grain.hidden = true;
      return;
    }
    canvas.hidden = !settings;
    grain.hidden = !settings || settings.grain === 0;
    grain.style.opacity = String(((settings?.grain || 0) / 100) * 0.018);
    grain.style.mixBlendMode = dark ? "screen" : "multiply";
    grain.classList.toggle(
      "is-moving",
      !!settings && settings.grain > 0 && !reduced.matches && !doc.hidden,
    );
    if (!settings) {
      stop();
      return;
    }
    renderSettings = { ...settings, grain: 0 };
    if (settings.mode !== mode && !lost) {
      renderer?.dispose();
      renderer = null;
      mode = settings.mode;
      failed = false;
      // Constant-fold unused designs out of the GPU program.
      try {
        renderer = createRenderer(
          canvas,
          vertex,
          fragment
            .replace(",u_mode,", ",")
            .replace(",u_grain,", ",")
            .replace(
              "#define PI",
              "const float u_mode=" +
                mode.toFixed(1) +
                ";\nconst float u_grain=0.;\n#define PI",
            ),
        );
      } catch (e) {
        failed = true;
        canvas.hidden = true;
        grain.hidden = true;
        return;
      }
    }
    const rect = canvas.getBoundingClientRect();
    if (!rect.width || !rect.height) return;
    const d = Math.min(win.devicePixelRatio || 1, 2) * settings.quality;
    const budget = mobile.matches ? 900000 : 2400000;
    const cap = Math.min(
      1,
      2200 / (rect.width * d),
      Math.sqrt(budget / (rect.width * rect.height * d * d)),
    );
    const w = Math.max(1, Math.round(rect.width * d * cap)),
      h = Math.max(1, Math.round(rect.height * d * cap));
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w;
      canvas.height = h;
    }
    const main = doc.querySelector("#main")?.getBoundingClientRect();
    if (main)
      reading = [
        (main.left - rect.left) / rect.width,
        (main.right - rect.left) / rect.width,
      ];
    dirty = true;
    wake();
  }
  function stop() {
    win.cancelAnimationFrame(raf);
    win.clearTimeout(timer);
    raf = timer = 0;
    last = 0;
  }
  function tick(now) {
    raf = 0;
    if (doc.hidden || lost || !settings || !renderer) return;
    const dt = last ? Math.min((now - last) / 1000, 0.1) : 0;
    last = now;
    const moving =
      !reduced.matches && settings.speed > 0 && settings.presence > 0;
    if (moving) time += dt * 2 * Math.pow(settings.speed / 100, 2);
    if (dirty || moving) {
      renderer.draw(renderSettings, time, dark ? 1 : 0, undefined, reading, 0);
      dirty = false;
    }
    // Grain is a separately composited texture; only shape motion needs WebGL frames.
    if (moving)
      timer = win.setTimeout(() => {
        timer = 0;
        wake();
      }, 1000 / 30);
    else last = 0;
  }
  const observer = new win.ResizeObserver(update);
  observer.observe(canvas);
  const themeObserver = new win.MutationObserver(update);
  themeObserver.observe(doc.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  const visibility = () => {
    stop();
    update();
  };
  doc.addEventListener("visibilitychange", visibility);
  for (const m of [reduced, scheme, mobile])
    m.addEventListener("change", update);
  win.addEventListener("resize", update);
  canvas.addEventListener("webglcontextlost", (e) => {
    e.preventDefault();
    lost = true;
    stop();
  });
  canvas.addEventListener("webglcontextrestored", () => {
    lost = false;
    failed = false;
    renderer = null;
    mode = -1;
    update();
  });
  update();
  return {
    update,
    destroy() {
      destroyed = true;
      stop();
      observer.disconnect();
      themeObserver.disconnect();
      doc.removeEventListener("visibilitychange", visibility);
      for (const m of [reduced, scheme, mobile])
        m.removeEventListener("change", update);
      win.removeEventListener("resize", update);
      renderer?.dispose(true);
      canvas.remove();
      grain.remove();
    },
  };
}
const configElement = document.querySelector('meta[name="dt-background"]');
if (configElement && !location.pathname.startsWith("/admin/")) {
  try {
    const config = JSON.parse(configElement.content);
    if (config.enabled)
      mountBackground(
        document,
        (mobile, dark) =>
          config.profiles[
            (mobile ? "mobile" : "desktop") + (dark ? "Dark" : "Light")
          ],
      );
  } catch {
    /* The plain site remains usable without WebGL. */
  }
}
