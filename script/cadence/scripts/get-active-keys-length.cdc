pub fun main(addr: Address): Int {
    let acct = getAccount(addr)
    var count = 0
    var index = 0
    while true {
        let key = acct.keys.get(keyIndex: index)
        if let val = key {
            if !val.isRevoked {
                count = count + 1
            }
            index = index + 1
        } else {
            return count
        }
    }
    return 0
}