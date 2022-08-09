import os

def clone_flowgo_cmd():
    return "git clone https://github.com/onflow/flow-go.git"
    searchfile = open("go.mod", "r")
    for line in searchfile:
        if "/onflow/flow-go " in line:
            split = line.split(" ")
            repo = split[0][1:]
            version = split[1][:-1]
            cmd = "git clone -b " + version + " --single-branch https://" + repo + ".git"
            return cmd

def main():
    clone_flowgo = clone_flowgo_cmd()
    os.system(clone_flowgo)

if __name__ == "__main__":
    main()
