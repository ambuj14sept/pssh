{
  pkgs ? import <nixpkgs>,
  lib ? import <nixpkgs/lib>,
}:

pkgs.writeShellApplication {
  name = "pssh";
  text = lib.readFile ./pssh;
  checkPhase = "true";

  meta = with lib; {
    description = "This is a fzf preview script to preview files in fzf";
    license = licenses.wtfpl;
    platforms = platforms.unix;
    maintainers = with maintainers; [ niksingh710 ];
    mainProgram = "pssh";
  };
}
