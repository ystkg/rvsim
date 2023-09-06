    addi    x5, x0, 2047
    lui     x6, 0x87
    addi    x6, x6, -1162
    addi    x10, x5, 0          # function argument
    addi    x11, x6, 0          # function argument
    jal     mulu                # call mul
    addi    x10, x10, 1224
    sw      x10, (x0)           # store "RISC"
    addi    x28, x0, 86
    addi    x29, x28, 0
    addi    x29, x29, -41
    slli    x28, x28, 8
    add     x7, x28, x29
    sh      x7, 4(x0)           # store "-V"
    jal     x0, end             # exit
mulu:                           # x10 = x10 * x11 (ignore overflow version)
    addi    x28, x0, 0
    bgeu    x10, x11, mulu_loop
    addi    x29, x10, 0         # if x10 < x11 then swap
    addi    x10, x11, 0
    addi    x11, x29, 0
mulu_loop:
    andi    x29, x11, 1
    beq     x29, x0, mulu_skip
    add     x28, x28, x10       # ignore overflow
mulu_skip:
    slli    x10, x10, 1         # ignore overflow
    srli    x11, x11, 1
    bne     x11, x0, mulu_loop
    addi    x10, x28, 0         # return value
    jalr    x0, (x1)            # ret
end:
